package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// psi writes the three /proc/pressure files with the "some" totals, in
// microseconds.
func (s *server) psi(cpu, memory, io uint64) {
	for name, total := range map[string]uint64{"cpu": cpu, "memory": memory, "io": io} {
		s.write("proc/pressure/"+name, fmt.Sprintf("some avg10=1.00 avg60=2.00 avg300=3.00 total=%d\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=7\n", total))
	}
}

func TestParsePSI(t *testing.T) {
	n, err := parsePSI([]byte("some avg10=0.12 avg60=0.34 avg300=0.56 total=987654321\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=5\n"))
	if err != nil || n != 987654321 {
		t.Errorf("parsePSI() = %d, %v; want the total of the some line", n, err)
	}
	// The CPU file of an older kernel has no full line.
	if n, err := parsePSI([]byte("some avg10=0.00 avg60=0.00 avg300=0.00 total=42\n")); err != nil || n != 42 {
		t.Errorf("parsePSI() = %d, %v; want 42", n, err)
	}
	for _, bad := range []string{"", "full avg10=0.00 total=5\n", "some avg10=0.00\n", "some total=x\n"} {
		if _, err := parsePSI([]byte(bad)); err == nil {
			t.Errorf("parsePSI(%q) = nil error, want an error", bad)
		}
	}
}

// pressureMinute takes a sample at base, five readings and the tick at
// base + 60 s. totals are the CPU, memory and I/O totals at each of the seven
// readings.
func pressureMinute(t *testing.T, srv *server, c *Collector, base time.Time, totals [7][3]uint64) wire.Sample {
	t.Helper()
	srv.psi(totals[0][0], totals[0][1], totals[0][2])
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 6; i++ {
		srv.psi(totals[i][0], totals[i][1], totals[i][2])
		c.Read(base.Add(time.Duration(i) * 10 * time.Second))
	}
	srv.psi(totals[6][0], totals[6][1], totals[6][2])
	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPressure(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)

	// The CPU waits 5 s in the window from 20 s to 30 s: 50% of that window,
	// and 6 s of the minute: 10%. The memory never waits. The I/O waits
	// 0.6 s in each window: 1%.
	s := pressureMinute(t, srv, c, base, [7][3]uint64{
		{1000, 0, 0},
		{201000, 0, 100000},
		{401000, 0, 200000},
		{5401000, 0, 300000},
		{5601000, 0, 400000},
		{5801000, 0, 500000},
		{6001000, 0, 600000},
	})

	want := map[string][2]float64{"cpu": {10, 50}, "memory": {0, 0}, "io": {1, 1}}
	got := map[string][2]*float64{
		"cpu":    {s.CPUPressurePercent, s.CPUPressureMaxPercent},
		"memory": {s.MemoryPressurePercent, s.MemoryPressureMaxPercent},
		"io":     {s.IOPressurePercent, s.IOPressureMaxPercent},
	}
	for name, w := range want {
		g := got[name]
		if g[0] == nil || g[1] == nil || !near(*g[0], w[0]) || !near(*g[1], w[1]) {
			t.Errorf("%s pressure = %v, max %v; want %v, max %v", name, ptr(g[0]), ptr(g[1]), w[0], w[1])
		}
	}
}

func TestPressureIsNotKnown(t *testing.T) {
	tests := []struct {
		name   string
		change func(*server)
		after  time.Duration
	}{
		{"no /proc/pressure", func(s *server) {
			if err := os.RemoveAll(filepath.Join(s.root, "proc/pressure")); err != nil {
				s.t.Fatal(err)
			}
		}, time.Minute},
		{"reboot", func(s *server) { s.write("proc/sys/kernel/random/boot_id", "boot-2\n") }, time.Minute},
		{"counter went back", func(s *server) { s.psi(10, 10, 10) }, time.Minute},
		{"previous reading too old", func(*server) {}, 3 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t)
			srv.psi(1000, 1000, 1000)
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
			checkNoPressure(t, s)
		})
	}
}

func TestFirstSampleHasNoPressure(t *testing.T) {
	srv := newServer(t)
	srv.psi(1000, 1000, 1000)
	c := srv.collector(t.TempDir())
	srv.psi(2000, 2000, 2000)

	s, err := c.Sample(time.Now().Add(30 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	checkNoPressure(t, s)
}

func TestPressureContinuesAfterARestart(t *testing.T) {
	srv := newServer(t)
	srv.psi(1000, 1000, 1000)
	state := t.TempDir()
	now := time.Now()
	if _, err := srv.collector(state).Sample(now.Add(-30 * time.Second)); err != nil {
		t.Fatal(err)
	}

	c := srv.collector(state)
	srv.psi(601000, 1000, 1000)
	s, err := c.Sample(now.Add(30 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPressurePercent == nil || !near(*s.CPUPressurePercent, 1) {
		t.Errorf("cpu_pressure_percent = %v, want 1 from the saved reading", ptr(s.CPUPressurePercent))
	}
}

func checkNoPressure(t *testing.T, s wire.Sample) {
	t.Helper()
	for _, v := range []*float64{s.CPUPressurePercent, s.CPUPressureMaxPercent, s.MemoryPressurePercent, s.MemoryPressureMaxPercent, s.IOPressurePercent, s.IOPressureMaxPercent} {
		if v != nil {
			t.Errorf("a pressure field = %v, want null", *v)
		}
	}
}

// near reports whether a is b, up to the rounding of the microseconds.
func near(a, b float64) bool {
	return a > b-0.001 && a < b+0.001
}

func TestAReadingWithoutPressureJoinsTheWindows(t *testing.T) {
	srv := newServer(t)
	srv.psi(0, 0, 0)
	c := srv.collector(t.TempDir())
	base := time.Now().Add(time.Second)
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}

	// The CPU waits 8 s from 0 s to 20 s, but the reading at 10 s has no
	// PSI: the peak is 8 s of the joined window of 20 s.
	srv.write("proc/pressure/cpu", "")
	c.Read(base.Add(10 * time.Second))
	srv.psi(8000000, 0, 0)
	c.Read(base.Add(20 * time.Second))
	srv.psi(9200000, 0, 0)

	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPressureMaxPercent == nil || !near(*s.CPUPressureMaxPercent, 40) {
		t.Errorf("cpu_pressure_max_percent = %v, want 40 from the joined window of 0 s to 20 s", ptr(s.CPUPressureMaxPercent))
	}
}
