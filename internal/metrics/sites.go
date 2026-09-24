package metrics

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
)

const (
	// workingDirLabel is the label that Docker Compose puts on each container
	// of a project: the folder of the project.
	workingDirLabel = "com.docker.compose.project.working_dir"

	// The disk use of each project folder is measured at most each hour, in
	// the background, and a walk of one folder stops after 5 minutes.
	sitesDiskEvery   = time.Hour
	sitesDiskTimeout = 5 * time.Minute
)

// containerUsage is the CPU time of one container, in microseconds, at a tick.
type containerUsage struct {
	project string
	usec    uint64
}

// containerReadings are the CPU times of the containers at one tick.
type containerReadings struct {
	bootID string
	at     time.Time
	usage  map[string]containerUsage // by container id
}

// homeDir returns the home folder of the user that runs the agent: the
// server user. It is "" when it is not known.
func homeDir() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return filepath.Clean(u.HomeDir)
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Clean(h)
	}
	return ""
}

// sites measures each Docker Compose project in the home folder at the tick
// cur (contract v0.5.0). It returns nil when the agent cannot read Docker or
// the cgroups: Docker does not run, the socket refuses the agent, or the
// server has cgroup v1. It returns an empty, non-nil slice when no project
// matches.
func (c *Collector) sites(ctx context.Context, cur reading) []wire.Site {
	if c.home == "" {
		return nil
	}
	if _, err := os.Stat(c.file("sys/fs/cgroup/cgroup.controllers")); err != nil {
		// cgroup v1, or no cgroups: the agent reads only cgroup v2.
		return nil
	}
	containers, err := c.docker.Containers(ctx)
	if err != nil {
		c.log.Debug("listing the containers", "error", err)
		return nil
	}

	type project struct {
		cpuUsec uint64
		cpuOK   bool
		mem     uint64
		memOK   bool
	}
	projects := map[string]*project{}
	now := containerReadings{bootID: cur.BootID, at: cur.At, usage: map[string]containerUsage{}}
	prev := c.prevContainers
	usable := prev != nil && prev.bootID == cur.BootID && cur.At.After(prev.at) && cur.At.Sub(prev.at) <= maxAge

	for _, ct := range containers {
		dir := filepath.Clean(ct.Labels[workingDirLabel])
		if ct.Labels[workingDirLabel] == "" || filepath.Dir(dir) != c.home {
			continue
		}
		name := filepath.Base(dir)
		p := projects[name]
		if p == nil {
			p = &project{}
			projects[name] = p
		}

		cg := c.cgroupDir(ct.ID)
		if cg == "" {
			continue
		}
		if usec, err := parseFile(c, filepath.Join(cg, "cpu.stat"), parseCPUStat); err == nil {
			now.usage[ct.ID] = containerUsage{project: name, usec: usec}
			// Only a container that both ticks saw, with the same id: a
			// container that started or restarted in the minute is left out.
			if old, ok := prev.lookup(ct.ID); usable && ok && old.project == name && usec >= old.usec {
				p.cpuUsec += usec - old.usec
				p.cpuOK = true
			}
		}
		if mem, err := c.containerMemory(cg); err == nil {
			p.mem += mem
			p.memOK = true
		}
	}
	c.prevContainers = &now

	cpus, cpuErr := parseFile(c, "proc/stat", parseCPUCount)
	disk := c.walker.take()
	out := make([]wire.Site, 0, len(projects))
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for name := range projects {
			if !yield(name) {
				return
			}
		}
	}) {
		p := projects[name]
		site := wire.Site{Directory: name}
		if p.cpuOK && cpuErr == nil {
			us := float64(cur.At.Sub(prev.at).Microseconds()) * float64(cpus)
			v := min(float64(p.cpuUsec)/us*100, 100)
			site.CPUPercent = &v
		}
		if p.memOK {
			site.MemoryUsedBytes = &p.mem
		}
		if d, ok := disk[name]; ok {
			site.DiskUsedBytes = &d
		}
		out = append(out, site)
	}

	// Measure the disk use of the folders in the background, at most each
	// hour. The result goes into the next sample.
	folders := map[string]string{}
	for name := range projects {
		folders[name] = filepath.Join(c.home, name)
	}
	c.walker.start(folders, c.log)

	return out
}

// lookup returns the reading of a container at the tick before.
func (r *containerReadings) lookup(id string) (containerUsage, bool) {
	if r == nil {
		return containerUsage{}, false
	}
	u, ok := r.usage[id]
	return u, ok
}

// cgroupDir returns the cgroup v2 folder of a container, relative to the
// root, or "". The systemd cgroup driver (the default of Ubuntu) and the
// cgroupfs driver use different folders.
func (c *Collector) cgroupDir(id string) string {
	if id == "" || strings.ContainsAny(id, "/.") {
		return ""
	}
	for _, dir := range []string{
		filepath.Join("sys/fs/cgroup/system.slice", "docker-"+id+".scope"),
		filepath.Join("sys/fs/cgroup/docker", id),
	} {
		if _, err := os.Stat(c.file(dir, "cpu.stat")); err == nil {
			return dir
		}
	}
	return ""
}

// containerMemory returns memory.current − inactive_file of a cgroup, as
// docker stats shows it on cgroup v2. The page cache that the kernel can drop
// is not used memory. A negative value counts as 0.
func (c *Collector) containerMemory(cg string) (uint64, error) {
	current, err := parseFile(c, filepath.Join(cg, "memory.current"), func(data []byte) (uint64, error) {
		return strconv.ParseUint(string(bytes.TrimSpace(data)), 10, 64)
	})
	if err != nil {
		return 0, err
	}
	inactive, err := parseFile(c, filepath.Join(cg, "memory.stat"), statValue("inactive_file"))
	if err != nil {
		return 0, err
	}
	return current - min(inactive, current), nil
}

// parseCPUStat reads usage_usec from a cgroup v2 cpu.stat.
func parseCPUStat(data []byte) (uint64, error) {
	return statValue("usage_usec")(data)
}

// statValue returns a parser for the value of key in a cgroup file of
// "key value" lines.
func statValue(key string) func([]byte) (uint64, error) {
	return func(data []byte) (uint64, error) {
		s := bufio.NewScanner(bytes.NewReader(data))
		for s.Scan() {
			k, v, ok := strings.Cut(s.Text(), " ")
			if ok && k == key {
				return strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			}
		}
		return 0, fmt.Errorf("no %s", key)
	}
}

// diskWalker measures the disk use of the project folders in a goroutine of
// its own, so that a large folder does not delay the samples. The collector
// takes each result one time.
type diskWalker struct {
	mu      sync.Mutex
	running bool
	last    time.Time         // the start of the last walk
	results map[string]uint64 // by directory, not yet sent
	// done is closed when the running walk ends. Tests wait for it.
	done chan struct{}
	// walk measures one folder. Tests replace it.
	walk func(ctx context.Context, root string) (uint64, int, error)
}

func newDiskWalker() *diskWalker {
	return &diskWalker{walk: diskUsage}
}

// start begins a walk of the folders, by directory, when no walk runs and the
// last walk started one hour ago or more.
func (w *diskWalker) start(folders map[string]string, log interface {
	Warn(msg string, args ...any)
}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running || len(folders) == 0 || (!w.last.IsZero() && time.Since(w.last) < sitesDiskEvery) {
		return
	}
	w.running, w.last = true, time.Now()
	done := make(chan struct{})
	w.done = done

	go func() {
		defer close(done)
		// The walk runs at the lowest I/O priority, so that it does not
		// slow the sites. The priority is a property of the thread: the
		// thread stays locked, and ends with the goroutine.
		lowerIOPriority()

		results := map[string]uint64{}
		for _, name := range slices.Sorted(func(yield func(string) bool) {
			for name := range folders {
				if !yield(name) {
					return
				}
			}
		}) {
			ctx, cancel := context.WithTimeout(context.Background(), sitesDiskTimeout)
			used, skipped, err := w.walk(ctx, folders[name])
			cancel()
			if err != nil {
				log.Warn("cannot measure the disk use of a site; sending null", "directory", name, "error", err)
				continue
			}
			if skipped > 0 {
				log.Warn("some files of a site cannot be read; its disk use is lower than the real use", "directory", name, "skipped", skipped)
			}
			results[name] = used
		}

		w.mu.Lock()
		defer w.mu.Unlock()
		w.results, w.running = results, false
	}()
}

// take returns the results of the last walk that ended, one time.
func (w *diskWalker) take() map[string]uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	r := w.results
	w.results = nil
	return r
}
