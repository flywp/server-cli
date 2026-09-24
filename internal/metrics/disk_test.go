package metrics

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// A /proc/diskstats of a DigitalOcean server with Ubuntu 24.04: two disks,
// the partitions of vda, and the loop devices of snap.
const diskstats = `   7       0 loop0 11 0 28 0 0 0 0 0 0 0 0 0 0 0 0 0 0
   7       1 loop1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
 253       0 vda 33376 8239 2465787 22011 165725 77576 2598846 818732 0 202221 889331 2747 0 2299640 1263 22315 47324
 253       1 vda1 32655 7544 2428229 21737 165698 77555 2598618 818558 0 211841 841559 2747 0 2299640 1263 0 0
 253      14 vda14 217 0 1984 58 0 0 0 0 0 52 58 0 0 0 0 0 0
 253      15 vda15 212 661 18510 80 2 0 2 3 0 52 83 0 0 0 0 0 0
 259       0 vda16 192 34 13224 109 25 21 226 169 0 253 279 0 0 0 0 0 0
 253      16 vdb 101 3 792 12 0 0 0 0 0 12 12 0 0 0 0 0 0
`

// blockDevice marks a disk in /sys/block as a hardware device.
func (s *server) blockDevice(name string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Join(s.root, "sys/block", name, "device"), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

// disks writes /proc/diskstats with the counters of each disk, and a loop
// device and a partition with large counters.
func (s *server) disks(disks map[string]diskCounters) {
	var b strings.Builder
	fmt.Fprintf(&b, "   7       0 loop0 99999 0 99999 0 99999 0 99999 0 0 0 0 0 0 0 0 0 0\n")
	for _, name := range slices.Sorted(maps.Keys(disks)) {
		d := disks[name]
		fmt.Fprintf(&b, " 253       0 %s %d 0 %d 0 %d 0 %d 0 0 0 0 0 0 0 0 0 0\n", name, d.ReadOps, d.ReadSectors, d.WriteOps, d.WriteSectors)
	}
	fmt.Fprintf(&b, " 253       1 vda1 99999 0 99999 0 99999 0 99999 0 0 0 0 0 0 0 0 0 0\n")
	s.write("proc/diskstats", b.String())
}

func TestParseDiskstats(t *testing.T) {
	all, err := parseDiskstats([]byte(diskstats))
	if err != nil {
		t.Fatal(err)
	}
	want := diskCounters{ReadOps: 33376, ReadSectors: 2465787, WriteOps: 165725, WriteSectors: 2598846}
	if all["vda"] != want {
		t.Errorf("vda = %+v, want %+v", all["vda"], want)
	}
	if len(all) != 8 {
		t.Errorf("%d lines, want 8", len(all))
	}
	// A kernel before 4.18 has 14 columns: no discards and flushes.
	all, err = parseDiskstats([]byte(" 8 0 sda 1 2 3 4 5 6 7 8 9 10 11\n"))
	if err != nil || all["sda"] != (diskCounters{ReadOps: 1, ReadSectors: 3, WriteOps: 5, WriteSectors: 7}) {
		t.Errorf("parseDiskstats(14 columns) = %+v, %v", all, err)
	}
	if _, err := parseDiskstats([]byte(" 8 0 sda 1 x 3 4 5 6 7 8 9 10 11\n")); err != nil {
		t.Errorf("parseDiskstats() = %v, want no error: a column that is not used is not examined", err)
	}
	if _, err := parseDiskstats([]byte(" 8 0 sda x 2 3 4 5 6 7 8 9 10 11\n")); err == nil {
		t.Error("parseDiskstats() = nil error, want an error for a bad count")
	}
}

func TestOnlyHardwareDisksAreCounted(t *testing.T) {
	srv := newServer(t)
	srv.write("proc/diskstats", diskstats)
	srv.blockDevice("vda")
	srv.blockDevice("vdb")
	// A loop device is in /sys/block, but it has no device.
	if err := os.MkdirAll(filepath.Join(srv.root, "sys/block/loop0"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := slices.Sorted(maps.Keys(srv.collector(t.TempDir()).readDisks()))
	if fmt.Sprint(got) != "[vda vdb]" {
		t.Errorf("disks = %v, want [vda vdb]", got)
	}
}

// diskMinute takes a sample at base, five readings and the tick at base +
// 60 s, with the counters of vda at each of the seven readings.
func diskMinute(t *testing.T, srv *server, c *Collector, base time.Time, vda [7]diskCounters) wire.Sample {
	t.Helper()
	srv.disks(map[string]diskCounters{"vda": vda[0]})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 6; i++ {
		srv.disks(map[string]diskCounters{"vda": vda[i]})
		c.Read(base.Add(time.Duration(i) * 10 * time.Second))
	}
	srv.disks(map[string]diskCounters{"vda": vda[6]})
	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDiskActivity(t *testing.T) {
	srv := newServer(t)
	srv.blockDevice("vda")
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	// Each window reads 10 operations of 20 sectors, and writes 100
	// operations of 200 sectors. The window from 30 s to 40 s writes 5000
	// operations of 100000 sectors.
	var vda [7]diskCounters
	for i := 1; i < 7; i++ {
		vda[i] = vda[i-1]
		vda[i].ReadOps += 10
		vda[i].ReadSectors += 20
		if i == 4 {
			vda[i].WriteOps += 5000
			vda[i].WriteSectors += 100000
		} else {
			vda[i].WriteOps += 100
			vda[i].WriteSectors += 200
		}
	}
	s := diskMinute(t, srv, c, base, vda)

	for _, f := range []struct {
		name string
		got  *uint64
		want uint64
	}{
		{"disk_read_bytes", s.DiskReadBytes, 120 * 512},
		{"disk_write_bytes", s.DiskWriteBytes, 101000 * 512},
		{"disk_read_ops", s.DiskReadOps, 60},
		{"disk_write_ops", s.DiskWriteOps, 5500},
		{"disk_read_max_bytes_per_second", s.DiskReadMaxBytesPerSecond, 20 * 512 / 10},
		{"disk_write_max_bytes_per_second", s.DiskWriteMaxBytesPerSecond, 100000 * 512 / 10},
		{"disk_read_max_ops_per_second", s.DiskReadMaxOpsPerSecond, 1},
		{"disk_write_max_ops_per_second", s.DiskWriteMaxOpsPerSecond, 500},
	} {
		if f.got == nil || *f.got != f.want {
			t.Errorf("%s = %v, want %d", f.name, ptr(f.got), f.want)
		}
	}
	checkDiskOrder(t, s)
}

func TestDiskActivityIsNotKnown(t *testing.T) {
	tests := []struct {
		name   string
		change func(*server)
		after  time.Duration
	}{
		{"no hardware disk", func(s *server) {
			if err := os.RemoveAll(filepath.Join(s.root, "sys/block")); err != nil {
				s.t.Fatal(err)
			}
		}, time.Minute},
		{"no /proc/diskstats", func(s *server) {
			if err := os.Remove(filepath.Join(s.root, "proc/diskstats")); err != nil {
				s.t.Fatal(err)
			}
		}, time.Minute},
		{"reboot", func(s *server) { s.write("proc/sys/kernel/random/boot_id", "boot-2\n") }, time.Minute},
		{"counter went back", func(s *server) { s.disks(map[string]diskCounters{"vda": {ReadOps: 1}}) }, time.Minute},
		{"previous reading too old", func(*server) {}, 3 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t)
			srv.blockDevice("vda")
			srv.disks(map[string]diskCounters{"vda": {ReadOps: 100, ReadSectors: 100, WriteOps: 100, WriteSectors: 100}})
			c := srv.collector(t.TempDir())
			now := time.Now().Add(time.Second)
			if _, err := c.Sample(now); err != nil {
				t.Fatal(err)
			}

			tt.change(srv)
			s, err := c.Sample(now.Add(tt.after))
			if err != nil {
				t.Fatal(err)
			}
			checkNoDiskActivity(t, s)
		})
	}
}

func TestACounterThatWentBackInAWindowMakesTheMinuteNotKnown(t *testing.T) {
	srv := newServer(t)
	srv.blockDevice("vda")
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	// vda is attached again at 20 s, with the same name: its counter
	// starts from 0. At the tick it is above the tick before again, but the
	// delta of the minute is not the real activity.
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 1000}})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	c.Read(base.Add(10 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 5}})
	c.Read(base.Add(20 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 2000}})

	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	checkNoDiskActivity(t, s)
}

func TestFirstSampleHasNoDiskActivity(t *testing.T) {
	srv := newServer(t)
	srv.blockDevice("vda")
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 100}})
	c := srv.collector(t.TempDir())
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 200}})

	s, err := c.Sample(time.Now().Add(30 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	checkNoDiskActivity(t, s)
}

func TestNewDiskMakesNoSpike(t *testing.T) {
	srv := newServer(t)
	srv.blockDevice("vda")
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	srv.disks(map[string]diskCounters{"vda": {ReadOps: 100}})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	// A volume is attached at 30 s, with counters from its own start.
	srv.blockDevice("sdb")
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 130}, "sdb": {ReadOps: 1 << 40}})
	c.Read(base.Add(30 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {ReadOps: 160}, "sdb": {ReadOps: 1<<40 + 30}})

	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskReadOps == nil || *s.DiskReadOps != 60 {
		t.Errorf("disk_read_ops = %v, want 60: the new sdb counts from the next sample", ptr(s.DiskReadOps))
	}
	if s.DiskReadMaxOpsPerSecond == nil || *s.DiskReadMaxOpsPerSecond != 2 {
		t.Errorf("disk_read_max_ops_per_second = %v, want 2: 30 of vda and 30 of sdb in 30 s", ptr(s.DiskReadMaxOpsPerSecond))
	}
	checkDiskOrder(t, s)
}

func TestAReadingWithoutDisksJoinsTheWindows(t *testing.T) {
	srv := newServer(t)
	srv.blockDevice("vda")
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	srv.disks(map[string]diskCounters{"vda": {}})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	// /proc/diskstats cannot be read at 20 s. The other counters can.
	if err := os.Remove(filepath.Join(srv.root, "proc/diskstats")); err != nil {
		t.Fatal(err)
	}
	c.Read(base.Add(20 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {WriteOps: 400}})
	c.Read(base.Add(40 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {WriteOps: 600}})

	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskWriteMaxOpsPerSecond == nil || *s.DiskWriteMaxOpsPerSecond != 10 {
		t.Errorf("disk_write_max_ops_per_second = %v, want 10: 400 in 40 s, or 200 in 20 s", ptr(s.DiskWriteMaxOpsPerSecond))
	}

	// Now the busy window is before the reading without disks: 600 in the
	// joined window of 40 s is 15 each second.
	if err := os.Remove(filepath.Join(srv.root, "proc/diskstats")); err != nil {
		t.Fatal(err)
	}
	c.Read(base.Add(80 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {WriteOps: 1200}})
	c.Read(base.Add(100 * time.Second))
	srv.disks(map[string]diskCounters{"vda": {WriteOps: 1200}})
	s, err = c.Sample(base.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.DiskWriteMaxOpsPerSecond == nil || *s.DiskWriteMaxOpsPerSecond != 15 {
		t.Errorf("disk_write_max_ops_per_second = %v, want 15 from the joined window of 60 s to 100 s", ptr(s.DiskWriteMaxOpsPerSecond))
	}
}

func checkNoDiskActivity(t *testing.T, s wire.Sample) {
	t.Helper()
	for _, v := range []*uint64{s.DiskReadBytes, s.DiskWriteBytes, s.DiskReadOps, s.DiskWriteOps,
		s.DiskReadMaxBytesPerSecond, s.DiskWriteMaxBytesPerSecond, s.DiskReadMaxOpsPerSecond, s.DiskWriteMaxOpsPerSecond} {
		if v != nil {
			t.Errorf("a disk activity field = %d, want null", *v)
		}
	}
}

// checkDiskOrder checks that a value of the minute is at most 60 times its
// peak, up to rounding.
func checkDiskOrder(t *testing.T, s wire.Sample) {
	t.Helper()
	for _, p := range [][2]*uint64{
		{s.DiskReadBytes, s.DiskReadMaxBytesPerSecond},
		{s.DiskWriteBytes, s.DiskWriteMaxBytesPerSecond},
		{s.DiskReadOps, s.DiskReadMaxOpsPerSecond},
		{s.DiskWriteOps, s.DiskWriteMaxOpsPerSecond},
	} {
		if p[0] == nil || p[1] == nil || *p[0] > 60**p[1]+59 {
			t.Errorf("value %v, peak %v: want value ≤ 60 × peak", ptr(p[0]), ptr(p[1]))
		}
	}
}
