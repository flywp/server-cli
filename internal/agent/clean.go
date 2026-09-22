package agent

import (
	"math"
	"time"
	"unicode/utf8"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// The value ranges of the contract (section 4, "Value ranges", and section 6).
// A value outside its range makes the control plane refuse the whole request
// with a 400, and the agent then drops up to 240 samples. So the agent keeps
// each value in its range before the value goes into a queue.
const (
	maxLoad          = 999999.99
	maxVersionLen    = 32
	maxStatusTextLen = 255
	maxArchLen       = 16
	maxEventNameLen  = 64
	maxErrorLen      = 2000
)

func cleanSample(s wire.Sample) wire.Sample {
	s.RecordedAt = s.RecordedAt.UTC().Truncate(time.Second)
	s.CPUPercent = clamp(s.CPUPercent, 0, 100)
	s.Load1 = clamp(s.Load1, 0, maxLoad)
	return s
}

func cleanStatus(s wire.Status) wire.Status {
	s.OS = truncate(s.OS, maxStatusTextLen)
	s.Kernel = truncate(s.Kernel, maxStatusTextLen)
	s.Arch = truncate(s.Arch, maxArchLen)
	return s
}

func cleanEvent(e wire.Event) wire.Event {
	e.Name = truncate(e.Name, maxEventNameLen)
	if e.Data != nil {
		d := *e.Data
		d.Version = truncate(d.Version, maxVersionLen)
		d.Error = truncate(d.Error, maxErrorLen)
		e.Data = &d
	}
	return e
}

// clamp keeps v between lo and hi. NaN becomes lo.
func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return min(max(v, lo), hi)
}

// truncate cuts s to at most n characters. The control plane counts
// characters, not bytes.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
