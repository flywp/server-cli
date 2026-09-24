package metrics

import (
	"context"
	"log/slog"
	"runtime"
	"testing"
	"time"
)

// TestRealServer measures this Linux machine: CI runs it on ubuntu-latest.
func TestRealServer(t *testing.T) {
	c := New("/", t.TempDir(), slog.New(slog.DiscardHandler))
	// The sample comes right after the start: measure it anyway.
	c.minFirst = 0

	s, err := c.Sample(time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if s.CPUPercent < 0 || s.CPUPercent > 100 {
		t.Errorf("cpu_percent = %v, want 0 to 100", s.CPUPercent)
	}
	if s.MemoryTotalBytes == 0 || s.MemoryUsedBytes > s.MemoryTotalBytes {
		t.Errorf("memory = %d of %d", s.MemoryUsedBytes, s.MemoryTotalBytes)
	}
	if s.DiskTotalBytes == 0 || s.DiskUsedBytes > s.DiskTotalBytes {
		t.Errorf("disk = %d of %d", s.DiskUsedBytes, s.DiskTotalBytes)
	}

	st := c.Status(context.Background())
	if st.Kernel == "" || st.Arch != runtime.GOARCH || st.UptimeSeconds == 0 {
		t.Errorf("status = %+v, want the kernel, the arch and the uptime", st)
	}
}
