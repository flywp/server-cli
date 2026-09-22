package metrics

import (
	"testing"
)

func TestParseCPU(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	got, err := parseCPU([]byte("cpu  100 10 50 800 40 0 0 0 30 0\ncpu0 1 1 1 1 1 1 1 1 1 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	// Guest time is already in user time, so it is not added again.
	if got.Total != 1000 || got.Idle != 840 {
		t.Errorf("parseCPU() = %+v, want total 1000 and idle 840", got)
	}

	for _, bad := range []string{"", "intr 1 2 3", "cpu  a b c d"} {
		if _, err := parseCPU([]byte(bad)); err == nil {
			t.Errorf("parseCPU(%q) = nil error, want an error", bad)
		}
	}
}

func TestCPUPercent(t *testing.T) {
	tests := []struct {
		prev, cur cpuTimes
		want      float64
	}{
		{cpuTimes{Idle: 800, Total: 1000}, cpuTimes{Idle: 950, Total: 1600}, 75},
		{cpuTimes{Idle: 800, Total: 1000}, cpuTimes{Idle: 1400, Total: 1600}, 0},
		{cpuTimes{Idle: 800, Total: 1000}, cpuTimes{Idle: 800, Total: 1600}, 100},
		// No time passed, or the counters went back.
		{cpuTimes{Idle: 800, Total: 1000}, cpuTimes{Idle: 800, Total: 1000}, 0},
		{cpuTimes{Idle: 800, Total: 1000}, cpuTimes{Idle: 10, Total: 20}, 0},
	}

	for _, tt := range tests {
		if got := cpuPercent(tt.prev, tt.cur); got != tt.want {
			t.Errorf("cpuPercent(%+v, %+v) = %v, want %v", tt.prev, tt.cur, got, tt.want)
		}
	}
}

func TestParseMeminfoNeedsMemAvailable(t *testing.T) {
	if _, err := parseMeminfo([]byte("MemTotal: 100 kB\nMemFree: 50 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")); err == nil {
		t.Error("parseMeminfo() = nil error, want an error without MemAvailable")
	}
}

func TestParseNetDev(t *testing.T) {
	data := "Inter-|   Receive |  Transmit\n face |bytes packets|bytes\n" +
		"  eth0: 1234 5 0 0 0 0 0 0 5678 6 0 0 0 0 0 0\n" +
		"    lo:1 1 0 0 0 0 0 0 2 2 0 0 0 0 0 0\n"

	got, err := parseNetDev([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if got["eth0"] != (netCounters{In: 1234, Out: 5678}) || got["lo"] != (netCounters{In: 1, Out: 2}) || len(got) != 2 {
		t.Errorf("parseNetDev() = %+v", got)
	}
}

func TestParseDefaultRoute(t *testing.T) {
	data := "Iface\tDestination\tGateway\neth1\t0A000000\t00000000\neth0\t00000000\t01C0A8C0\n"
	if got := parseDefaultRoute([]byte(data)); got != "eth0" {
		t.Errorf("parseDefaultRoute() = %q, want eth0", got)
	}
	if got := parseDefaultRoute([]byte("Iface\tDestination\n")); got != "" {
		t.Errorf("parseDefaultRoute() = %q, want no interface", got)
	}
}

func TestParseOSRelease(t *testing.T) {
	tests := map[string]string{
		"NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n": "Ubuntu 24.04.1 LTS",
		"PRETTY_NAME='Debian GNU/Linux 12'\n":                   "Debian GNU/Linux 12",
		"PRETTY_NAME=Alpine\n":                                  "Alpine",
		"NAME=x\n":                                              "",
	}
	for in, want := range tests {
		if got := parseOSRelease([]byte(in)); got != want {
			t.Errorf("parseOSRelease(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseUptime(t *testing.T) {
	if got, err := parseUptime([]byte("350735.47 234388.90\n")); err != nil || got != 350735 {
		t.Errorf("parseUptime() = %d, %v; want 350735", got, err)
	}
	if _, err := parseUptime([]byte("")); err == nil {
		t.Error("parseUptime(\"\") = nil error, want an error")
	}
}

func TestParseAptCheck(t *testing.T) {
	if total, security, err := parseAptCheck([]byte("33;6")); err != nil || total != 33 || security != 6 {
		t.Errorf("parseAptCheck(33;6) = %d, %d, %v", total, security, err)
	}
	for _, bad := range []string{"", "33", "a;b", "1;x"} {
		if _, _, err := parseAptCheck([]byte(bad)); err == nil {
			t.Errorf("parseAptCheck(%q) = nil error, want an error", bad)
		}
	}
}
