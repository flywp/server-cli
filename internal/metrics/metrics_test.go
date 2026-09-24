package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent"
	"github.com/flywp/server-cli/internal/agent/wire"
)

// server is a fake file system root with the files that the collector reads.
type server struct {
	t    *testing.T
	root string
}

func newServer(t *testing.T) *server {
	t.Helper()

	s := &server{t: t, root: t.TempDir()}
	s.write("proc/loadavg", "0.42 0.30 0.25 1/345 6789\n")
	s.write("proc/meminfo", "MemTotal:        8000000 kB\nMemFree:          500000 kB\nMemAvailable:    6000000 kB\nSwapTotal:       2000000 kB\nSwapFree:        1500000 kB\n")
	s.write("proc/uptime", "1892344.51 3700000.00\n")
	s.write("proc/sys/kernel/random/boot_id", "boot-1\n")
	s.write("proc/net/route", "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"+
		"ens3\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"+
		"ens3\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n")
	s.write("etc/os-release", "NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nID=ubuntu\n")
	s.device("eth0")
	s.device("eth1")
	s.cpu(1000, 800)
	s.net(map[string][2]uint64{"eth0": {1000, 500}, "eth1": {100, 50}, "lo": {9999, 9999}, "docker0": {7000, 7000}, "veth1": {7000, 7000}})
	return s
}

func (s *server) write(name, content string) {
	s.t.Helper()
	path := filepath.Join(s.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// device marks an interface as a hardware device.
func (s *server) device(name string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Join(s.root, "sys/class/net", name, "device"), 0o755); err != nil {
		s.t.Fatal(err)
	}
}

// cpu writes /proc/stat with the total and the idle ticks.
func (s *server) cpu(total, idle uint64) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	busy := total - idle
	s.write("proc/stat", fmt.Sprintf("cpu  %d 0 0 %d 0 0 0 0 55 0\ncpu0 1 2 3 4 5 6 7 8 9 10\n", busy, idle))
}

// net writes /proc/net/dev with the received and sent bytes of each interface.
func (s *server) net(ifaces map[string][2]uint64) {
	var b strings.Builder
	b.WriteString("Inter-|   Receive                                                |  Transmit\n")
	b.WriteString(" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n")
	for name, c := range ifaces {
		fmt.Fprintf(&b, "%6s: %d 10 0 0 0 0 0 0 %d 10 0 0 0 0 0 0\n", name, c[0], c[1])
	}
	s.write("proc/net/dev", b.String())
}

func (s *server) collector(stateDir string) *Collector {
	c := New(s.root, stateDir, slog.New(slog.DiscardHandler))
	c.statfs = func(string) (uint64, uint64, error) { return 100 << 30, 25 << 30, nil }
	c.release = func() string { return "6.8.0-45-generic" }
	c.aptCheck = func(context.Context) ([]byte, error) { return []byte("33;6"), nil }
	// The tests take the first sample some milliseconds after the start.
	c.minFirst = 0
	return c
}

func TestSample(t *testing.T) {
	srv := newServer(t)
	now := time.Now()
	c := srv.collector(t.TempDir())
	if _, err := c.Sample(now); err != nil {
		t.Fatal(err)
	}

	// One minute later: 600 more ticks with 150 idle, and some traffic.
	srv.cpu(1600, 950)
	srv.net(map[string][2]uint64{"eth0": {3000, 1500}, "eth1": {600, 150}, "lo": {99999, 99999}, "docker0": {70000, 70000}, "veth1": {70000, 70000}})

	s, err := c.Sample(now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if s.CPUPercent != 75 {
		t.Errorf("cpu_percent = %v, want 75 (450 of 600 ticks busy)", s.CPUPercent)
	}
	if s.Load1 != 0.42 {
		t.Errorf("load_1 = %v, want 0.42", s.Load1)
	}
	// MemTotal − MemAvailable, not MemTotal − MemFree.
	if s.MemoryTotalBytes != 8000000*1024 || s.MemoryUsedBytes != 2000000*1024 {
		t.Errorf("memory = %d of %d, want %d of %d", s.MemoryUsedBytes, s.MemoryTotalBytes, 2000000*1024, 8000000*1024)
	}
	if s.SwapTotalBytes != 2000000*1024 || s.SwapUsedBytes != 500000*1024 {
		t.Errorf("swap = %d of %d, want %d of %d", s.SwapUsedBytes, s.SwapTotalBytes, 500000*1024, 2000000*1024)
	}
	if s.DiskTotalBytes != 100<<30 || s.DiskUsedBytes != 25<<30 {
		t.Errorf("disk = %d of %d, want the statfs values", s.DiskUsedBytes, s.DiskTotalBytes)
	}
	// Only eth0 and eth1 have a device: lo, docker0 and veth1 are not counted.
	if s.NetInBytes != 2000+500 || s.NetOutBytes != 1000+100 || s.NetCountersReset {
		t.Errorf("net = in %d, out %d, reset %v; want in 2500, out 1100 from eth0 and eth1", s.NetInBytes, s.NetOutBytes, s.NetCountersReset)
	}
}

func TestFirstSampleHasNoTraffic(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())

	s, err := c.Sample(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !s.NetCountersReset || s.NetInBytes != 0 || s.NetOutBytes != 0 {
		t.Errorf("first sample net = %d, %d, reset %v; want 0, 0 and a reset", s.NetInBytes, s.NetOutBytes, s.NetCountersReset)
	}
}

func TestRestartContinuesFromTheSavedCounters(t *testing.T) {
	srv := newServer(t)
	state := t.TempDir()
	now := time.Now()
	if _, err := srv.collector(state).Sample(now); err != nil {
		t.Fatal(err)
	}

	// A new agent process starts, and one minute after the last sample it
	// takes the next one.
	srv.net(map[string][2]uint64{"eth0": {1500, 700}, "eth1": {100, 50}})
	s, err := srv.collector(state).Sample(now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.NetCountersReset || s.NetInBytes != 500 || s.NetOutBytes != 200 {
		t.Errorf("net after a restart = %d, %d, reset %v; want 500, 200 without a reset", s.NetInBytes, s.NetOutBytes, s.NetCountersReset)
	}
}

func TestTrafficResets(t *testing.T) {
	tests := []struct {
		name   string
		change func(*server)
		after  time.Duration
	}{
		{"reboot", func(s *server) { s.write("proc/sys/kernel/random/boot_id", "boot-2\n") }, time.Minute},
		{"counter went back", func(s *server) { s.net(map[string][2]uint64{"eth0": {10, 10}, "eth1": {100, 50}}) }, time.Minute},
		{"previous reading too old", func(*server) {}, 3 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t)
			now := time.Now()
			c := srv.collector(t.TempDir())
			if _, err := c.Sample(now); err != nil {
				t.Fatal(err)
			}

			tt.change(srv)
			s, err := c.Sample(now.Add(tt.after))
			if err != nil {
				t.Fatal(err)
			}
			if !s.NetCountersReset || s.NetInBytes != 0 || s.NetOutBytes != 0 {
				t.Errorf("net = %d, %d, reset %v; want 0, 0 and a reset", s.NetInBytes, s.NetOutBytes, s.NetCountersReset)
			}
		})
	}
}

func TestStaleSavedCountersAreIgnored(t *testing.T) {
	srv := newServer(t)
	state := t.TempDir()
	if _, err := srv.collector(state).Sample(time.Now().Add(-10 * time.Minute)); err != nil {
		t.Fatal(err)
	}

	// The agent was stopped for 10 minutes: its saved traffic counters do not
	// give the traffic of one minute.
	srv.net(map[string][2]uint64{"eth0": {900000, 900000}, "eth1": {100, 50}})
	s, err := srv.collector(state).Sample(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !s.NetCountersReset || s.NetInBytes != 0 {
		t.Errorf("net = %d, reset %v; want 0 and a reset", s.NetInBytes, s.NetCountersReset)
	}
}

func TestNewInterfaceMakesNoSpike(t *testing.T) {
	srv := newServer(t)
	now := time.Now()
	c := srv.collector(t.TempDir())
	if _, err := c.Sample(now); err != nil {
		t.Fatal(err)
	}

	srv.device("eth2")
	srv.net(map[string][2]uint64{"eth0": {1100, 600}, "eth1": {100, 50}, "eth2": {5 << 40, 5 << 40}})
	s, err := c.Sample(now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.NetInBytes != 100 || s.NetOutBytes != 100 {
		t.Errorf("net = %d, %d; want 100, 100 (the new eth2 counts from the next sample)", s.NetInBytes, s.NetOutBytes)
	}
}

func TestInterfacesFallBackToTheDefaultRoute(t *testing.T) {
	srv := newServer(t)
	if err := os.RemoveAll(filepath.Join(srv.root, "sys")); err != nil {
		t.Fatal(err)
	}
	srv.net(map[string][2]uint64{"ens3": {1, 1}, "lo": {1, 1}, "docker0": {1, 1}})

	all, err := parseFile(srv.collector(t.TempDir()), "proc/net/dev", parseNetDev)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.collector(t.TempDir()).interfaces(all); fmt.Sprint(got) != "[ens3]" {
		t.Errorf("interfaces() = %v, want [ens3] from the default route", got)
	}
}

func TestSampleErrors(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	if err := os.Remove(filepath.Join(srv.root, "proc/meminfo")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sample(time.Now()); err == nil {
		t.Error("Sample() = nil error, want an error without /proc/meminfo")
	}

	srv = newServer(t)
	c = srv.collector(t.TempDir())
	c.statfs = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	if _, err := c.Sample(time.Now()); err == nil {
		t.Error("Sample() = nil error, want the statfs error")
	}
}

func TestStatus(t *testing.T) {
	srv := newServer(t)
	srv.write("var/run/reboot-required", "*** System restart required ***\n")
	c := srv.collector(t.TempDir())

	calls := 0
	c.aptCheck = func(context.Context) ([]byte, error) {
		calls++
		return []byte("33;6"), nil
	}

	// The count runs with the first sample, before the sends of a report.
	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}
	s := c.Status(context.Background())
	if !s.RebootRequired || !counts(s, 33, 6) {
		t.Errorf("status = %+v, want a restart and 33 updates with 6 security updates", s)
	}
	if s.OS != "Ubuntu 24.04.1 LTS" || s.Kernel != "6.8.0-45-generic" || s.UptimeSeconds != 1892344 || s.Arch != runtime.GOARCH {
		t.Errorf("status = %+v", s)
	}

	// The counts stay for one hour: apt-check takes some seconds.
	c.Status(context.Background())
	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("apt-check ran %d times, want 1 in one hour", calls)
	}

	c.updatesAt = time.Now().Add(-2 * time.Hour)
	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("apt-check ran %d times, want again after one hour", calls)
	}
}

func TestStatusWithoutAptCheck(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	c.aptCheck = func(context.Context) ([]byte, error) { return nil, os.ErrNotExist }

	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}
	s := c.Status(context.Background())
	// Not known is null, not a false "no updates".
	if s.UpdatesTotal != nil || s.UpdatesSecurity != nil || s.RebootRequired {
		t.Errorf("status = %+v, want unknown (nil) updates and no restart", s)
	}
	if s.OS == "" {
		t.Error("status has no OS: one missing value must not clear the others")
	}
}

func TestAptCheckWithWarningsBeforeTheResult(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	// apt-check on Ubuntu 24.04 with a source that is configured two times.
	c.aptCheck = func(context.Context) ([]byte, error) {
		return []byte("/usr/lib/update-notifier/apt-check:351: Warning: W:Target Packages (main/binary-amd64/Packages) is configured multiple times in /etc/apt/sources.list:1 and /etc/apt/sources.list.d/ubuntu.sources:1\n" +
			"  apt_pkg.init()\n29;26"), nil
	}

	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := c.Status(context.Background()); !counts(s, 29, 26) {
		t.Errorf("updates = %v;%v, want 29;26 from the last line", s.UpdatesTotal, s.UpdatesSecurity)
	}
}

func TestAFailedCountKeepsTheLastCounts(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}

	// One hour later apt-check fails, for example with a timeout.
	c.aptCheck = func(context.Context) ([]byte, error) { return nil, context.DeadlineExceeded }
	c.updatesAt = time.Now().Add(-2 * time.Hour)
	if _, err := c.Sample(time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := c.Status(context.Background()); !counts(s, 33, 6) {
		t.Errorf("updates = %v;%v, want the last counts 33;6", s.UpdatesTotal, s.UpdatesSecurity)
	}
}

// counts reports whether s has the known update counts total and security.
func counts(s wire.Status, total, security uint64) bool {
	return s.UpdatesTotal != nil && s.UpdatesSecurity != nil && *s.UpdatesTotal == total && *s.UpdatesSecurity == security
}

func TestNoCountedInterfaceIsNotZeroTraffic(t *testing.T) {
	srv := newServer(t)
	if err := os.RemoveAll(filepath.Join(srv.root, "sys")); err != nil {
		t.Fatal(err)
	}
	// An IPv6-only host: no IPv4 default route.
	srv.write("proc/net/route", "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n")
	now := time.Now()
	c := srv.collector(t.TempDir())
	if _, err := c.Sample(now); err != nil {
		t.Fatal(err)
	}

	s, err := c.Sample(now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !s.NetCountersReset {
		t.Error("net_counters_reset = false, want true: the traffic is not known")
	}
}

func TestPortsOfAnOtherInterfaceAreNotCounted(t *testing.T) {
	srv := newServer(t)
	// Azure accelerated networking: the VF has a device, but its traffic is
	// also in eth0, its master. A bond port is the same.
	srv.device("enP1s1")
	if err := os.Symlink("../eth0", filepath.Join(srv.root, "sys/class/net/enP1s1/master")); err != nil {
		t.Fatal(err)
	}
	srv.net(map[string][2]uint64{"eth0": {1000, 500}, "eth1": {100, 50}, "enP1s1": {900, 400}})

	all, err := parseFile(srv.collector(t.TempDir()), "proc/net/dev", parseNetDev)
	if err != nil {
		t.Fatal(err)
	}
	got := srv.collector(t.TempDir()).interfaces(all)
	sort.Strings(got)
	if fmt.Sprint(got) != "[eth0 eth1]" {
		t.Errorf("interfaces() = %v, want [eth0 eth1] without the port enP1s1", got)
	}
}

func TestNewWithCountersThatCannotBeRead(t *testing.T) {
	srv := newServer(t)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "counters.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := srv.collector(state).Sample(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !s.NetCountersReset {
		t.Error("net_counters_reset = false, want true after counters that cannot be read")
	}
}

func TestAShortFirstMinuteHasNoSample(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	c.minFirst = minFirstMinute

	// The install ends 1 s before the tick: that second is all busy.
	srv.cpu(1100, 800)
	first := time.Now().Add(time.Second)
	if _, err := c.Sample(first); !errors.Is(err, agent.ErrNoSample) {
		t.Fatalf("Sample() 1 s after the start = %v, want ErrNoSample", err)
	}

	// The next minute is a whole minute from the tick reading, with its
	// traffic.
	srv.cpu(1700, 1250)
	srv.net(map[string][2]uint64{"eth0": {1600, 800}, "eth1": {100, 50}})
	s, err := c.Sample(first.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.CPUPercent != 25 {
		t.Errorf("cpu_percent = %v, want 25 from the tick reading, not from the start", s.CPUPercent)
	}
	if s.NetCountersReset || s.NetInBytes != 600 || s.NetInMaxBytesPerSecond == nil {
		t.Errorf("net = %d, peak %v, reset %v; want 600 and a peak: the minute has a previous reading", s.NetInBytes, ptr(s.NetInMaxBytesPerSecond), s.NetCountersReset)
	}
}

func TestALongFirstMinuteHasASample(t *testing.T) {
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	c.minFirst = minFirstMinute

	srv.cpu(1600, 950)
	s, err := c.Sample(time.Now().Add(20 * time.Second))
	if err != nil {
		t.Fatalf("Sample() 20 s after the start = %v, want a sample", err)
	}
	if s.CPUPercent != 75 {
		t.Errorf("cpu_percent = %v, want 75", s.CPUPercent)
	}
}

func TestARestartWithSavedCountersHasASample(t *testing.T) {
	srv := newServer(t)
	state := t.TempDir()
	now := time.Now()
	if _, err := srv.collector(state).Sample(now.Add(-59 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// A quick restart, for example an update, 1 s before the tick: the
	// minute continues from the saved reading.
	c := srv.collector(state)
	c.minFirst = minFirstMinute
	if _, err := c.Sample(now.Add(time.Second)); err != nil {
		t.Errorf("Sample() after a restart with saved counters = %v, want a sample", err)
	}
}
