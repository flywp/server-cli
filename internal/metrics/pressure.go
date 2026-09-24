package metrics

import (
	"slices"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// psiTotals are the "some" totals of /proc/pressure, in microseconds.
type psiTotals struct {
	CPU    uint64 `json:"cpu"`
	Memory uint64 `json:"memory"`
	IO     uint64 `json:"io"`
}

// readPSI reads the three pressure files. It returns nil when the kernel has
// no PSI: no /proc/pressure, or a kernel booted with psi=0, where a read
// fails. It logs this one time.
func (c *Collector) readPSI() *psiTotals {
	var t psiTotals
	for _, f := range []struct {
		name string
		v    *uint64
	}{{"cpu", &t.CPU}, {"memory", &t.Memory}, {"io", &t.IO}} {
		n, err := parseFile(c, "proc/pressure/"+f.name, parsePSI)
		if err != nil {
			if !c.noPSI {
				c.noPSI = true
				c.log.Warn("no pressure (PSI) to send: the kernel has no PSI, or it is off", "error", err)
			}
			return nil
		}
		*f.v = n
	}

	return &t
}

// pressure returns the share of the time between two readings in which at
// least one task waited for the CPU, the memory and the disk, 0 to 100. ok is
// false when the share is not known: a reading without PSI, a reboot, a
// reading older than maxAge, or a counter that went back.
func pressure(a, b reading) (cpu, mem, io float64, ok bool) {
	if a.PSI == nil || b.PSI == nil || a.BootID != b.BootID {
		return 0, 0, 0, false
	}
	d := b.At.Sub(a.At)
	if d <= 0 || d > maxAge {
		return 0, 0, 0, false
	}
	if b.PSI.CPU < a.PSI.CPU || b.PSI.Memory < a.PSI.Memory || b.PSI.IO < a.PSI.IO {
		return 0, 0, 0, false
	}

	us := float64(d.Microseconds())
	share := func(from, to uint64) float64 {
		return min(float64(to-from)/us*100, 100)
	}
	return share(a.PSI.CPU, b.PSI.CPU), share(a.PSI.Memory, b.PSI.Memory), share(a.PSI.IO, b.PSI.IO), true
}

// setPressure sets the pressure of the minute from prev to cur in s, and the
// peak of the windows in all. The six fields stay nil when the pressure of the
// minute is not known.
func setPressure(s *wire.Sample, prev *reading, all []reading, cur reading) {
	if prev == nil {
		return
	}
	cpu, mem, io, ok := pressure(*prev, cur)
	if !ok {
		return
	}

	cpuMax, memMax, ioMax := cpu, mem, io
	// A reading without PSI is left out: its windows join.
	all = slices.DeleteFunc(slices.Clone(all), func(r reading) bool { return r.PSI == nil })
	for i := 1; i < len(all); i++ {
		if c, m, o, ok := pressure(all[i-1], all[i]); ok {
			cpuMax, memMax, ioMax = max(cpuMax, c), max(memMax, m), max(ioMax, o)
		}
	}

	s.CPUPressurePercent, s.CPUPressureMaxPercent = &cpu, &cpuMax
	s.MemoryPressurePercent, s.MemoryPressureMaxPercent = &mem, &memMax
	s.IOPressurePercent, s.IOPressureMaxPercent = &io, &ioMax
}
