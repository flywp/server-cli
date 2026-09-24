package metrics

import (
	"os"
	"slices"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// sectorSize is the unit of the sector counts in /proc/diskstats, for each
// disk, whatever the sector size of the disk.
const sectorSize = 512

// readDisks returns the counters of the disks that have a hardware device: a
// name in /sys/block with a device link, for example vda, sda or nvme0n1.
// Partitions are not in /sys/block, and loop, ram, zram, device mapper and
// software RAID disks have no device: their activity is already in a hardware
// disk. It returns nil when /proc/diskstats cannot be read.
func (c *Collector) readDisks() map[string]diskCounters {
	all, err := parseFile(c, "proc/diskstats", parseDiskstats)
	if err != nil {
		c.log.Debug("reading the disk activity", "error", err)
		return nil
	}

	disks := map[string]diskCounters{}
	for name, d := range all {
		if _, err := os.Lstat(c.file("sys/block", name, "device")); err == nil {
			disks[name] = d
		}
	}
	return disks
}

// diskDelta returns the activity between two readings. It adds the disks
// that both readings have, so a new or a removed disk makes no spike. ok is
// false when the activity is not known: a reading without disks, a reboot, a
// reading older than maxAge, or a counter that went back.
func diskDelta(a, b reading) (d diskCounters, ok bool) {
	if len(a.Disks) == 0 || len(b.Disks) == 0 || a.BootID != b.BootID {
		return diskCounters{}, false
	}
	if age := b.At.Sub(a.At); age <= 0 || age > maxAge {
		return diskCounters{}, false
	}

	for name, cur := range b.Disks {
		prev, found := a.Disks[name]
		if !found {
			continue
		}
		if cur.ReadOps < prev.ReadOps || cur.ReadSectors < prev.ReadSectors ||
			cur.WriteOps < prev.WriteOps || cur.WriteSectors < prev.WriteSectors {
			return diskCounters{}, false
		}
		d.ReadOps += cur.ReadOps - prev.ReadOps
		d.ReadSectors += cur.ReadSectors - prev.ReadSectors
		d.WriteOps += cur.WriteOps - prev.WriteOps
		d.WriteSectors += cur.WriteSectors - prev.WriteSectors
	}

	return d, true
}

// setDiskActivity sets the disk activity of the minute from prev to cur in s,
// and the peaks of the windows in all. The eight fields stay nil when the
// activity of the minute is not known, or when a window has a counter that
// went back: then the delta of the minute is not the real activity either.
func setDiskActivity(s *wire.Sample, prev *reading, all []reading, cur reading) {
	if prev == nil {
		return
	}
	d, ok := diskDelta(*prev, cur)
	if !ok {
		return
	}
	readBytes, writeBytes := d.ReadSectors*sectorSize, d.WriteSectors*sectorSize

	// The control plane reads the value of the minute as the value / 60.
	peak := [4]uint64{readBytes / 60, writeBytes / 60, d.ReadOps / 60, d.WriteOps / 60}
	// A reading without disks is left out: its windows join.
	all = slices.DeleteFunc(slices.Clone(all), func(r reading) bool { return len(r.Disks) == 0 })
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		w, ok := diskDelta(a, b)
		if !ok {
			return
		}
		secs := b.At.Sub(a.At).Seconds()
		for j, v := range []uint64{w.ReadSectors * sectorSize, w.WriteSectors * sectorSize, w.ReadOps, w.WriteOps} {
			peak[j] = max(peak[j], uint64(float64(v)/secs))
		}
	}

	s.DiskReadBytes, s.DiskWriteBytes = &readBytes, &writeBytes
	s.DiskReadOps, s.DiskWriteOps = &d.ReadOps, &d.WriteOps
	s.DiskReadMaxBytesPerSecond, s.DiskWriteMaxBytesPerSecond = &peak[0], &peak[1]
	s.DiskReadMaxOpsPerSecond, s.DiskWriteMaxOpsPerSecond = &peak[2], &peak[3]
}
