// Package metrics measures a Linux server for the monitoring agent: CPU,
// load, memory, swap, disk, network, pressure (PSI) and disk activity each
// minute, with the peaks of the minute from a reading each 10 seconds, the
// use of each site, and the status of the server (contract v0.5.0). It needs
// no root.
package metrics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/flywp/server-cli/internal/agent"
	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/dockerapi"
	"github.com/flywp/server-cli/internal/statefile"
)

const (
	// maxAge is the oldest previous reading that gives the traffic of one
	// minute. The agent samples each 60 seconds; an older reading covers
	// more than one minute.
	maxAge = 90 * time.Second

	// The update counts come from apt-check, which takes some seconds.
	updatesEvery    = time.Hour
	aptCheckPath    = "/usr/lib/update-notifier/apt-check"
	aptCheckTimeout = 30 * time.Second

	// minFirstMinute is the shortest first minute after a start. A shorter
	// one is mostly the load of the start, for example an install: its CPU
	// value would be a false spike.
	minFirstMinute = 10 * time.Second

	// maxReadings limits the readings between two ticks. The agent reads
	// five times between two ticks; more readings come only when ticks fail.
	maxReadings = 30
)

// reading holds the counters at one moment. The reading of the tick is saved,
// so that the first sample after an agent restart continues from it.
type reading struct {
	BootID string                 `json:"boot_id"`
	At     time.Time              `json:"at"`
	CPU    cpuTimes               `json:"cpu"`
	Net    map[string]netCounters `json:"net"`
	// PSI is nil when the kernel has no pressure information.
	PSI *psiTotals `json:"psi,omitempty"`
	// Disks is nil when /proc/diskstats cannot be read, and empty when no
	// disk has a hardware device.
	Disks map[string]diskCounters `json:"disks,omitempty"`
	// mem is not saved: only the readings of the minute give its peak.
	mem memory
}

// Collector measures the server. Use New.
type Collector struct {
	root string
	path string // counters.json
	log  *slog.Logger

	// statfs returns the size and the used space of the file system of a
	// path, and release the kernel release. Tests replace them.
	statfs   func(path string) (total, used uint64, err error)
	release  func() string
	aptCheck func(ctx context.Context) ([]byte, error)
	docker   *dockerapi.Client
	// home is the home folder of the server user, where the Docker Compose
	// projects of the sites are.
	home string

	// prev is the reading of the last tick, and readings are the readings
	// after it, oldest first. They give the windows of the next sample.
	prev     *reading
	readings []reading
	// fromStart is true while prev is the reading of the start of this
	// process, not a saved reading. minFirst is the shortest first minute;
	// tests set it to 0.
	fromStart bool
	minFirst  time.Duration
	// noInterface is true after the warning that no interface is counted,
	// and noPSI after the warning that the kernel has no PSI.
	noInterface bool
	noPSI       bool
	// dockerWarned is true after the warning that the state of Docker is
	// not known, until it is known again.
	dockerWarned bool

	// prevContainers are the CPU times of the containers at the last tick,
	// and walker measures the disk use of the sites.
	prevContainers *containerReadings
	walker         *diskWalker

	updatesAt       time.Time
	updatesKnown    bool
	updatesTotal    uint64
	updatesSecurity uint64
}

// New returns a collector that reads the files under root ("/" on a server)
// and keeps its counters in stateDir.
func New(root, stateDir string, log *slog.Logger) *Collector {
	c := &Collector{
		root:     root,
		path:     filepath.Join(stateDir, "counters.json"),
		log:      log,
		statfs:   statfs,
		release:  kernelRelease,
		aptCheck: runAptCheck,
		minFirst: minFirstMinute,
	}
	c.docker = dockerapi.New(c.file("var/run/docker.sock"))
	c.home = homeDir()
	c.walker = newDiskWalker()

	// Take a reading now, so that the first sample has a CPU value for the
	// time since the start. The saved reading comes before it only when it is
	// recent and from this boot: then the traffic continues without a gap.
	now := time.Now()
	start, startErr := c.read(now)

	var saved reading
	err := statefile.Read(c.path, &saved)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("ignoring the saved counters", "error", err)
	}

	switch {
	case startErr != nil:
		// The first sample has no previous reading.
	case err == nil && saved.BootID == start.BootID && saved.At.Before(now) && now.Sub(saved.At) <= maxAge:
		c.prev = &saved
		c.readings = []reading{start}
	default:
		// The traffic, the pressure and the disk activity since the start
		// are not those of one minute: the first sample sends them as not
		// known.
		start.Net, start.PSI, start.Disks = nil, nil, nil
		c.prev = &start
		c.fromStart = true
	}

	return c
}

// Read takes a reading between two ticks, for the peaks of the minute. A
// reading that fails is left out: the windows on each side of it join into
// one (contract v0.4.0).
func (c *Collector) Read(now time.Time) {
	if last := c.last(); last != nil && !now.After(last.At) {
		return
	}

	r, err := c.read(now)
	if err != nil {
		c.log.Debug("skipping a reading", "error", err)
		return
	}
	if len(c.readings) >= maxReadings {
		c.readings = slices.Delete(c.readings, 0, 1)
	}
	c.readings = append(c.readings, r)
}

// last returns the newest reading, or nil.
func (c *Collector) last() *reading {
	if n := len(c.readings); n > 0 {
		return &c.readings[n-1]
	}
	return c.prev
}

// Sample measures the minute that ends at now. At the first sample and then
// each hour, it also counts the waiting updates for Status: apt-check takes
// some seconds, and a sample comes before the sends of a report. The count
// comes after the reading of the tick, so that it does not move the reading.
func (c *Collector) Sample(now time.Time) (wire.Sample, error) {
	cur, err := c.read(now)
	if err != nil {
		return wire.Sample{}, err
	}

	// A tick right after the start has no minute to measure. Its reading
	// starts the next minute, which then has all its values.
	if c.fromStart {
		c.fromStart = false
		if d := cur.At.Sub(c.prev.At); d >= 0 && d < c.minFirst {
			c.prev, c.readings = &cur, nil
			if err := statefile.Write(c.path, cur); err != nil {
				c.log.Warn("saving the counters", "error", err)
			}
			return wire.Sample{}, fmt.Errorf("%w: the first minute is only %s since the start", agent.ErrNoSample, d.Round(time.Millisecond))
		}
	}

	// The containers come right after the reading of the tick: their CPU
	// times must be of the same moment.
	sites := c.sites(context.Background(), cur)

	c.refreshUpdates(context.Background())

	var s wire.Sample
	if s.Load1, err = parseFile(c, "proc/loadavg", parseLoad); err != nil {
		return wire.Sample{}, err
	}

	mem := cur.mem
	s.MemoryTotalBytes = mem.total
	s.MemoryUsedBytes = mem.total - min(mem.available, mem.total)
	s.SwapTotalBytes = mem.swapTotal
	s.SwapUsedBytes = mem.swapTotal - min(mem.swapFree, mem.swapTotal)

	if s.DiskTotalBytes, s.DiskUsedBytes, err = c.statfs(c.file("")); err != nil {
		return wire.Sample{}, err
	}

	prev := c.prev
	if prev != nil && prev.BootID == cur.BootID && cur.CPU.Total >= prev.CPU.Total {
		s.CPUPercent = cpuPercent(prev.CPU, cur.CPU)
	}
	s.NetInBytes, s.NetOutBytes, s.NetCountersReset = netDelta(prev, cur)
	if len(cur.Net) == 0 {
		// No interface is counted, so the traffic is not known: it is not 0.
		s.NetCountersReset = true
		if !c.noInterface {
			c.noInterface = true
			c.log.Warn("no network interface to count: no interface has a hardware device, and the default route has none")
		}
	}

	setPeaks(&s, c.prev, c.readings, cur)
	s.Sites = sites

	c.prev = &cur
	c.readings = nil
	if err := statefile.Write(c.path, cur); err != nil {
		c.log.Warn("saving the counters", "error", err)
	}

	return s, nil
}

// netDelta returns the traffic between two readings. It adds the interfaces
// that both readings have, so a new or a removed interface makes no spike.
// reset is true when the traffic of the minute is not known: no previous
// reading, a reboot, a reading older than maxAge, or a counter that went back.
func netDelta(prev *reading, cur reading) (in, out uint64, reset bool) {
	if prev == nil || prev.Net == nil || prev.BootID != cur.BootID {
		return 0, 0, true
	}
	if age := cur.At.Sub(prev.At); age <= 0 || age > maxAge {
		return 0, 0, true
	}

	for name, c := range cur.Net {
		p, ok := prev.Net[name]
		if !ok {
			continue
		}
		if c.In < p.In || c.Out < p.Out {
			return 0, 0, true
		}
		in += c.In - p.In
		out += c.Out - p.Out
	}

	return in, out, false
}

// read takes the counters now.
func (c *Collector) read(now time.Time) (reading, error) {
	cpu, err := parseFile(c, "proc/stat", parseCPU)
	if err != nil {
		return reading{}, err
	}

	mem, err := parseFile(c, "proc/meminfo", parseMeminfo)
	if err != nil {
		return reading{}, err
	}

	all, err := parseFile(c, "proc/net/dev", parseNetDev)
	if err != nil {
		return reading{}, err
	}

	net := map[string]netCounters{}
	for _, name := range c.interfaces(all) {
		net[name] = all[name]
	}

	bootID, err := os.ReadFile(c.file("proc/sys/kernel/random/boot_id"))
	if err != nil {
		return reading{}, err
	}

	return reading{BootID: string(bytes.TrimSpace(bootID)), At: now, CPU: cpu, Net: net, PSI: c.readPSI(), Disks: c.readDisks(), mem: mem}, nil
}

// interfaces returns the network interfaces that have a hardware device and
// are not a port of an other interface. Thus lo, docker0, the Docker bridges
// and the veth interfaces are left out, and container traffic is not counted
// two or three times. A port (of a bond or a bridge, or the Azure VF under
// its netvsc interface) is left out too, because its traffic is also in the
// interface above it. If no interface is left, it returns the interface of
// the default route, for example the bond or the bridge.
func (c *Collector) interfaces(all map[string]netCounters) []string {
	var names []string
	for name := range all {
		if _, err := os.Lstat(c.file("sys/class/net", name, "device")); err != nil {
			continue
		}
		if _, err := os.Lstat(c.file("sys/class/net", name, "master")); err == nil {
			continue
		}
		names = append(names, name)
	}
	if len(names) > 0 {
		return names
	}

	route, err := os.ReadFile(c.file("proc/net/route"))
	if err != nil {
		return nil
	}
	if name := parseDefaultRoute(route); name != "" {
		if _, ok := all[name]; ok {
			return []string{name}
		}
	}

	return nil
}

// Status describes the server now. A value that cannot be read stays empty,
// 0 or nil, and the problem goes to the log. The update counts come from the
// last Sample.
func (c *Collector) Status(ctx context.Context) wire.Status {
	s := wire.Status{Arch: runtime.GOARCH, Kernel: c.release()}

	if _, err := os.Stat(c.file("var/run/reboot-required")); err == nil {
		s.RebootRequired = true
	}

	if data, err := os.ReadFile(c.file("etc/os-release")); err == nil {
		s.OS = parseOSRelease(data)
	} else {
		c.log.Warn("reading the OS name", "error", err)
	}

	if up, err := parseFile(c, "proc/uptime", parseUptime); err == nil {
		s.UptimeSeconds = up
	} else {
		c.log.Warn("reading the uptime", "error", err)
	}

	if n, err := parseFile(c, "proc/stat", parseCPUCount); err == nil {
		s.CPUCount = &n
	} else {
		c.log.Warn("counting the CPUs", "error", err)
	}

	s.DockerStatus, s.DockerVersion = c.dockerStatus(ctx)

	// Without any count, the counts are not known: null, not a false 0.
	if c.updatesKnown {
		total, security := c.updatesTotal, c.updatesSecurity
		s.UpdatesTotal, s.UpdatesSecurity = &total, &security
	}

	return s
}

// refreshUpdates counts the waiting updates at the first call and then each
// hour. If apt-check fails, the last counts stay. Without any count, Status
// sends null (contract v0.3.1).
func (c *Collector) refreshUpdates(ctx context.Context) {
	if !c.updatesAt.IsZero() && time.Since(c.updatesAt) < updatesEvery {
		return
	}
	c.updatesAt = time.Now()

	ctx, cancel := context.WithTimeout(ctx, aptCheckTimeout)
	defer cancel()

	out, err := c.aptCheck(ctx)
	var total, security uint64
	if err == nil {
		total, security, err = parseAptCheck(out)
	}
	if err != nil {
		if c.updatesKnown {
			c.log.Warn("cannot count the waiting updates; keeping the last counts", "error", err)
		} else {
			c.log.Warn("cannot count the waiting updates; sending null", "error", err)
		}
		return
	}

	c.updatesKnown = true
	c.updatesTotal, c.updatesSecurity = total, security
}

// runAptCheck runs apt-check. It writes its result to stderr.
func runAptCheck(ctx context.Context) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, aptCheckPath)
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	return stderr.Bytes(), nil
}

// file returns the path of a file under the root.
func (c *Collector) file(parts ...string) string {
	return filepath.Join(append([]string{c.root}, parts...)...)
}

// parseFile reads a file under the root and parses it.
func parseFile[T any](c *Collector, name string, parse func([]byte) (T, error)) (T, error) {
	data, err := os.ReadFile(c.file(name))
	if err != nil {
		var zero T
		return zero, err
	}

	return parse(data)
}
