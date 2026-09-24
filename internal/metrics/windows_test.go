package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// mem writes /proc/meminfo with the available memory and the free swap, in
// kB. The totals are 8000000 kB and 2000000 kB.
func (s *server) mem(availableKB, swapFreeKB uint64) {
	s.write("proc/meminfo", fmt.Sprintf("MemTotal:        8000000 kB\nMemFree:          500000 kB\nMemAvailable:    %d kB\nSwapTotal:       2000000 kB\nSwapFree:        %d kB\n", availableKB, swapFreeKB))
}

// step is one step of a test minute: the counters that the server shows at
// a reading.
type step struct {
	total, idle uint64 // CPU ticks
	in, out     uint64 // eth0 bytes
	available   uint64 // kB
	swapFree    uint64 // kB
}

func (s *server) set(st step) {
	s.cpu(st.total, st.idle)
	s.net(map[string][2]uint64{"eth0": {st.in, st.out}, "eth1": {100, 50}})
	s.mem(st.available, st.swapFree)
}

// runMinute takes a sample at base, a reading each 10 seconds, and the
// sample of the next tick at base + 60 s. steps holds the counters of the
// five readings and of the tick.
func runMinute(t *testing.T, srv *server, c *Collector, base time.Time, first step, steps [6]step) wire.Sample {
	t.Helper()

	srv.set(first)
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		srv.set(steps[i])
		c.Read(base.Add(time.Duration(i+1) * 10 * time.Second))
	}
	srv.set(steps[5])
	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPeaks(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	// Each window adds 100 CPU ticks. The window from 20 s to 30 s is 90%
	// busy; the others are 10% busy. eth0 receives 10000 bytes in the
	// window from 40 s to 50 s, and 1000 bytes in each other window. The
	// memory peaks at 30 s, and the swap at 40 s.
	first := step{1000, 800, 0, 0, 6000000, 1500000}
	s := runMinute(t, srv, c, base, first, [6]step{
		{1100, 890, 1000, 100, 6000000, 1500000},
		{1200, 980, 2000, 200, 5000000, 1500000},
		{1300, 990, 3000, 300, 4000000, 1500000},
		{1400, 1080, 4000, 400, 5000000, 1000000},
		{1500, 1170, 14000, 500, 6000000, 1500000},
		{1600, 1260, 15000, 600, 6000000, 1500000},
	})

	// The minute: 600 ticks, 460 idle.
	if want := float64(140) / 600 * 100; s.CPUPercent != want {
		t.Errorf("cpu_percent = %v, want %v", s.CPUPercent, want)
	}
	if s.CPUMaxPercent == nil || *s.CPUMaxPercent != 90 {
		t.Errorf("cpu_max_percent = %v, want 90", ptr(s.CPUMaxPercent))
	}
	if want := uint64(4000000 * 1024); s.MemoryUsedMaxBytes == nil || *s.MemoryUsedMaxBytes != want {
		t.Errorf("memory_used_max_bytes = %v, want %d", ptr(s.MemoryUsedMaxBytes), want)
	}
	if want := uint64(1000000 * 1024); s.SwapUsedMaxBytes == nil || *s.SwapUsedMaxBytes != want {
		t.Errorf("swap_used_max_bytes = %v, want %d", ptr(s.SwapUsedMaxBytes), want)
	}
	if s.NetInMaxBytesPerSecond == nil || *s.NetInMaxBytesPerSecond != 1000 {
		t.Errorf("net_in_max_bytes_per_second = %v, want 1000", ptr(s.NetInMaxBytesPerSecond))
	}
	if s.NetOutMaxBytesPerSecond == nil || *s.NetOutMaxBytesPerSecond != 10 {
		t.Errorf("net_out_max_bytes_per_second = %v, want 10", ptr(s.NetOutMaxBytesPerSecond))
	}
	checkOrder(t, s)
}

func TestAFailedReadingJoinsTwoWindows(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	srv.set(step{1000, 800, 0, 0, 6000000, 1500000})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}

	// The reading at 10 s fails. The window from 0 s to 20 s has 190 idle
	// ticks of 200.
	stat := filepath.Join(srv.root, "proc/stat")
	if err := os.Remove(stat); err != nil {
		t.Fatal(err)
	}
	c.Read(base.Add(10 * time.Second))
	srv.set(step{1200, 990, 0, 0, 6000000, 1500000})
	c.Read(base.Add(20 * time.Second))
	srv.set(step{1300, 1000, 0, 0, 6000000, 1500000})

	s, err := c.Sample(base.Add(30 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUMaxPercent == nil || *s.CPUMaxPercent != 90 {
		t.Errorf("cpu_max_percent = %v, want 90 from the window of 20 s to 30 s", ptr(s.CPUMaxPercent))
	}
	if s.NetInMaxBytesPerSecond == nil || *s.NetInMaxBytesPerSecond != 0 {
		t.Errorf("net_in_max_bytes_per_second = %v, want 0", ptr(s.NetInMaxBytesPerSecond))
	}
}

func TestFirstSamplePeaksEqualTheMinute(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())

	// The agent started just now: one window, from the start to the tick.
	srv.set(step{1600, 950, 5000, 5000, 5000000, 1500000})
	s, err := c.Sample(time.Now().Add(20 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUMaxPercent == nil || *s.CPUMaxPercent != s.CPUPercent || s.CPUPercent != 75 {
		t.Errorf("cpu = %v, max %v; want 75 and an equal peak", s.CPUPercent, ptr(s.CPUMaxPercent))
	}
	if s.MemoryUsedMaxBytes == nil || *s.MemoryUsedMaxBytes != s.MemoryUsedBytes {
		t.Errorf("memory_used_max_bytes = %v, want the value of the minute %d", ptr(s.MemoryUsedMaxBytes), s.MemoryUsedBytes)
	}
	// The traffic since the start is not known, so its peak is not known.
	if !s.NetCountersReset || s.NetInMaxBytesPerSecond != nil || s.NetOutMaxBytesPerSecond != nil {
		t.Errorf("net peaks = %v, %v with reset %v; want null", ptr(s.NetInMaxBytesPerSecond), ptr(s.NetOutMaxBytesPerSecond), s.NetCountersReset)
	}
}

func TestPeaksAfterARestartIncludeTheSavedReading(t *testing.T) {
	srv := newServer(t)
	state := t.TempDir()
	now := time.Now()

	// The last tick of the old process was 30 s ago.
	srv.set(step{1000, 800, 0, 0, 6000000, 1500000})
	if _, err := srv.collector(state).Sample(now.Add(-30 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// The new process starts after 100% busy time. Then it takes the tick.
	srv.set(step{1100, 800, 3000, 0, 6000000, 1500000})
	c := srv.collector(state)
	srv.set(step{1600, 1250, 3000, 0, 6000000, 1500000})
	s, err := c.Sample(now.Add(30 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 25 {
		t.Errorf("cpu_percent = %v, want 25 from the saved reading", s.CPUPercent)
	}
	if s.CPUMaxPercent == nil || *s.CPUMaxPercent != 100 {
		t.Errorf("cpu_max_percent = %v, want 100 from the window of the saved reading to the start", ptr(s.CPUMaxPercent))
	}
	if s.NetCountersReset || s.NetInBytes != 3000 || s.NetInMaxBytesPerSecond == nil || *s.NetInMaxBytesPerSecond < 99 {
		t.Errorf("net = %d, peak %v, reset %v; want 3000 with a peak of about 100 each second (3000 bytes in 30 s)", s.NetInBytes, ptr(s.NetInMaxBytesPerSecond), s.NetCountersReset)
	}
	checkOrder(t, s)
}

func TestNetPeaksAreNullAfterACounterWentBack(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	// eth0 goes back at 30 s and forward again by the tick: the minute
	// looks correct, but a window is not known.
	s := runMinute(t, srv, c, base, step{1000, 800, 5000, 5000, 6000000, 1500000}, [6]step{
		{1100, 890, 6000, 6000, 6000000, 1500000},
		{1200, 980, 7000, 7000, 6000000, 1500000},
		{1300, 990, 10, 10, 6000000, 1500000},
		{1400, 1080, 1000, 1000, 6000000, 1500000},
		{1500, 1170, 6000, 6000, 6000000, 1500000},
		{1600, 1260, 9000, 9000, 6000000, 1500000},
	})
	if s.NetInMaxBytesPerSecond != nil || s.NetOutMaxBytesPerSecond != nil {
		t.Errorf("net peaks = %v, %v; want null after a window with a counter that went back", ptr(s.NetInMaxBytesPerSecond), ptr(s.NetOutMaxBytesPerSecond))
	}
	if s.CPUMaxPercent == nil {
		t.Error("cpu_max_percent = null, want a value: only the network is not known")
	}
}

func TestReadingsThatAreNotNewerAreIgnored(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	srv.set(step{1000, 800, 0, 0, 6000000, 1500000})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	c.Read(base.Add(20 * time.Second))
	// The wall clock stepped back: the same reading comes again, and an
	// older one.
	c.Read(base.Add(20 * time.Second))
	c.Read(base.Add(10 * time.Second))
	if len(c.readings) != 1 {
		t.Errorf("%d readings, want 1", len(c.readings))
	}

	// A reading close to the tick makes no window of some milliseconds.
	c.Read(base.Add(59*time.Second + 900*time.Millisecond))
	srv.set(step{1100, 810, 0, 0, 6000000, 1500000})
	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUMaxPercent == nil || *s.CPUMaxPercent != 90 {
		t.Errorf("cpu_max_percent = %v, want 90 from the window of 20 s to 60 s", ptr(s.CPUMaxPercent))
	}
}

func TestMemoryPeakAfterAFailedTickIsFromTheLastMinute(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	srv.set(step{1000, 800, 0, 0, 6000000, 1500000})
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	// A high use of memory at 30 s. The tick at 60 s fails.
	srv.mem(1000000, 1500000)
	c.Read(base.Add(30 * time.Second))
	srv.mem(6000000, 1500000)
	c.Read(base.Add(50 * time.Second))
	stat := filepath.Join(srv.root, "proc/stat")
	if err := os.Remove(stat); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sample(base.Add(time.Minute)); err == nil {
		t.Fatal("Sample() = nil error, want the error of the tick")
	}

	c.Read(base.Add(70 * time.Second))
	srv.set(step{1100, 900, 0, 0, 6000000, 1500000})
	s, err := c.Sample(base.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(2000000 * 1024); s.MemoryUsedMaxBytes == nil || *s.MemoryUsedMaxBytes != want {
		t.Errorf("memory_used_max_bytes = %v, want %d: the peak at 30 s is in the minute before", ptr(s.MemoryUsedMaxBytes), want)
	}
}

func TestReadingsAreLimited(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)
	for i := range 100 {
		c.Read(base.Add(time.Duration(i) * 10 * time.Second))
	}
	if len(c.readings) != maxReadings {
		t.Errorf("%d readings, want at most %d", len(c.readings), maxReadings)
	}
}

// checkOrder checks the order that the contract guarantees between a value of
// the minute and its peak.
func checkOrder(t *testing.T, s wire.Sample) {
	t.Helper()
	if s.CPUMaxPercent != nil && s.CPUPercent > *s.CPUMaxPercent {
		t.Errorf("cpu_percent %v > cpu_max_percent %v", s.CPUPercent, *s.CPUMaxPercent)
	}
	if m := s.MemoryUsedMaxBytes; m == nil || s.MemoryUsedBytes > *m || *m > s.MemoryTotalBytes {
		t.Errorf("memory %d, max %v, total %d: want used ≤ max ≤ total", s.MemoryUsedBytes, ptr(m), s.MemoryTotalBytes)
	}
	if m := s.SwapUsedMaxBytes; m == nil || s.SwapUsedBytes > *m || *m > s.SwapTotalBytes {
		t.Errorf("swap %d, max %v, total %d: want used ≤ max ≤ total", s.SwapUsedBytes, ptr(m), s.SwapTotalBytes)
	}
	if m := s.NetInMaxBytesPerSecond; m != nil && s.NetInBytes > 60**m+59 {
		t.Errorf("net_in_bytes %d > 60 × %d", s.NetInBytes, *m)
	}
	if m := s.NetOutMaxBytesPerSecond; m != nil && s.NetOutBytes > 60**m+59 {
		t.Errorf("net_out_bytes %d > 60 × %d", s.NetOutBytes, *m)
	}
}

// ptr shows a value that can be null.
func ptr[T any](v *T) any {
	if v == nil {
		return "null"
	}
	return *v
}
