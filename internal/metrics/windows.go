package metrics

import (
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// minWindow is the shortest window between two readings. A reading closer
// than this to its neighbours is left out, because a peak of some
// milliseconds is noise, not the peak of 10 seconds. Only the minute itself
// can be shorter, for example when the agent started just before the tick.
const minWindow = 5 * time.Second

// chain returns the readings that make the windows of the minute that ends
// at cur: prev (when it is before cur), the readings between, and cur, in
// time order. The times always go up, also after a step of the wall clock.
func chain(prev *reading, readings []reading, cur reading) []reading {
	var out []reading
	if prev != nil && prev.At.Before(cur.At) {
		out = append(out, *prev)
	}
	for _, r := range readings {
		if n := len(out); n > 0 && r.At.Sub(out[n-1].At) < minWindow {
			continue
		}
		if cur.At.Sub(r.At) < minWindow {
			continue
		}
		out = append(out, r)
	}

	return append(out, cur)
}

// setPeaks sets the peaks of the minute in s: the highest value of the
// windows from one reading to the next (contract v0.4.0). s already holds
// the values of the minute, from prev to cur. A peak is never less than the
// value of its minute.
func setPeaks(s *wire.Sample, prev *reading, readings []reading, cur reading) {
	all := chain(prev, readings, cur)

	// The memory peaks come from the readings of the minute: not from the
	// reading of the tick before, and not from an older minute whose tick
	// failed.
	var memMax, swapMax uint64
	for _, r := range all {
		if cur.At.Sub(r.At) >= time.Minute {
			continue
		}
		memMax = max(memMax, r.mem.total-min(r.mem.available, r.mem.total))
		swapMax = max(swapMax, r.mem.swapTotal-min(r.mem.swapFree, r.mem.swapTotal))
	}
	memMax = min(max(memMax, s.MemoryUsedBytes), s.MemoryTotalBytes)
	swapMax = min(max(swapMax, s.SwapUsedBytes), s.SwapTotalBytes)
	s.MemoryUsedMaxBytes, s.SwapUsedMaxBytes = &memMax, &swapMax

	if cpu, ok := cpuPeak(all); ok {
		cpu = max(cpu, s.CPUPercent)
		s.CPUMaxPercent = &cpu
	}

	if !s.NetCountersReset {
		if in, out, ok := netPeaks(all); ok {
			// The control plane reads the value of the minute as bytes / 60.
			in, out = max(in, s.NetInBytes/60), max(out, s.NetOutBytes/60)
			s.NetInMaxBytesPerSecond, s.NetOutMaxBytesPerSecond = &in, &out
		}
	}
}

// cpuPeak returns the busy share of the busiest window. A window across a
// reboot, with counters that went back, or of an older minute whose tick
// failed, is left out.
func cpuPeak(all []reading) (peak float64, ok bool) {
	cur := all[len(all)-1]
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		if cur.At.Sub(b.At) >= time.Minute {
			continue
		}
		if a.BootID != b.BootID || b.CPU.Total <= a.CPU.Total || b.CPU.Idle < a.CPU.Idle {
			continue
		}
		peak, ok = max(peak, cpuPercent(a.CPU, b.CPU)), true
	}

	return peak, ok
}

// netPeaks returns the received and sent bytes each second of the busiest
// windows, rounded down. It returns false when the traffic of a window is not
// known, for example after a counter went back.
func netPeaks(all []reading) (in, out uint64, ok bool) {
	for i := 1; i < len(all); i++ {
		a, b := all[i-1], all[i]
		dIn, dOut, reset := netDelta(&a, b)
		if reset {
			return 0, 0, false
		}
		secs := b.At.Sub(a.At).Seconds()
		in = max(in, uint64(float64(dIn)/secs))
		out = max(out, uint64(float64(dOut)/secs))
		ok = true
	}

	return in, out, ok
}
