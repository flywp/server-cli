package agent

import (
	"math"
	"time"
	"unicode/utf8"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/oklog/ulid/v2"
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
	for _, v := range []*uint64{
		&s.MemoryUsedBytes, &s.MemoryTotalBytes, &s.SwapUsedBytes, &s.SwapTotalBytes,
		&s.DiskUsedBytes, &s.DiskTotalBytes, &s.NetInBytes, &s.NetOutBytes,
	} {
		*v = clampInt(*v)
	}
	for _, v := range []**float64{
		&s.CPUMaxPercent, &s.CPUPressurePercent, &s.CPUPressureMaxPercent, &s.MemoryPressurePercent,
		&s.MemoryPressureMaxPercent, &s.IOPressurePercent, &s.IOPressureMaxPercent,
	} {
		*v = clampPtr(*v, 0, 100)
	}
	for _, v := range []**uint64{
		&s.MemoryUsedMaxBytes, &s.SwapUsedMaxBytes, &s.NetInMaxBytesPerSecond, &s.NetOutMaxBytesPerSecond,
	} {
		*v = clampIntPtr(*v)
	}
	return s
}

func cleanStatus(s wire.Status) wire.Status {
	s.OS = truncate(s.OS, maxStatusTextLen)
	s.Kernel = truncate(s.Kernel, maxStatusTextLen)
	s.Arch = truncate(s.Arch, maxArchLen)
	s.UpdatesTotal = clampIntPtr(s.UpdatesTotal)
	s.UpdatesSecurity = clampIntPtr(s.UpdatesSecurity)
	s.UptimeSeconds = clampInt(s.UptimeSeconds)
	return s
}

func cleanEvent(e wire.Event) wire.Event {
	// A command id that is not a ULID makes a 400, and a 400 drops all the
	// events of the request. Without the id, the event changes nothing.
	if e.CommandID != "" {
		if _, err := ulid.ParseStrict(e.CommandID); err != nil {
			e.CommandID = ""
		}
	}
	e.Name = truncate(e.Name, maxEventNameLen)
	if e.Data != nil {
		d := *e.Data
		d.Version = truncate(d.Version, maxVersionLen)
		d.Error = truncate(d.Error, maxErrorLen)
		e.Data = &d
	}
	return e
}

// clampInt keeps v in the range of a signed 64-bit integer: the control plane
// (PHP) cannot hold a larger integer, and fails the request with a 500. The
// agent would then send the same sample again for 24 hours.
func clampInt(v uint64) uint64 {
	return min(v, math.MaxInt64)
}

// clampIntPtr is clampInt for a value that can be "not known" (nil).
func clampIntPtr(v *uint64) *uint64 {
	if v == nil {
		return nil
	}
	c := clampInt(*v)
	return &c
}

// clamp keeps v between lo and hi. NaN becomes lo.
func clamp(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return min(max(v, lo), hi)
}

// clampPtr is clamp for a value that can be "not known" (nil).
func clampPtr(v *float64, lo, hi float64) *float64 {
	if v == nil {
		return nil
	}
	c := clamp(*v, lo, hi)
	return &c
}

// truncate cuts s to at most n characters. The control plane counts
// characters, not bytes.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
