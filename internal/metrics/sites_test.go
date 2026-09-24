package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/dockerapi"
	"github.com/flywp/server-cli/internal/testutil"
)

// fakeContainer is a running container of the fake Docker Engine.
type fakeContainer struct {
	id, dir string
}

// containerJSON is the answer of GET /containers/json for the containers.
func containerJSON(cs []fakeContainer) string {
	var items []string
	for _, c := range cs {
		labels := "{}"
		if c.dir != "" {
			labels = fmt.Sprintf(`{"com.docker.compose.project.working_dir":%q,"com.docker.compose.service":"php"}`, c.dir)
		}
		items = append(items, fmt.Sprintf(`{"Id":%q,"Names":["/%s"],"Labels":%s}`, c.id, c.id, labels))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// cgroup writes the cgroup v2 files of a container, with the systemd driver.
func (s *server) cgroup(id string, usageUsec, current, inactiveFile uint64) {
	s.cgroupAt(filepath.Join("sys/fs/cgroup/system.slice", "docker-"+id+".scope"), usageUsec, current, inactiveFile)
}

func (s *server) cgroupAt(dir string, usageUsec, current, inactiveFile uint64) {
	s.write("sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory pids\n")
	s.write(filepath.Join(dir, "cpu.stat"), fmt.Sprintf("usage_usec %d\nuser_usec 1\nsystem_usec 2\n", usageUsec))
	s.write(filepath.Join(dir, "memory.current"), fmt.Sprintf("%d\n", current))
	s.write(filepath.Join(dir, "memory.stat"), fmt.Sprintf("anon 1\nfile 2\nactive_file 3\ninactive_file %d\n", inactiveFile))
}

// sitesCollector returns a collector whose home is /home/fly under the root,
// and whose Docker Engine lists the containers. The disk walk returns 4096
// bytes for each folder.
func sitesCollector(t *testing.T, srv *server, cs []fakeContainer) *Collector {
	t.Helper()
	engine := testutil.NewEngine(t, testutil.EngineHandler("29.7.1", containerJSON(cs)))
	c := srv.collector(t.TempDir())
	c.docker = dockerapi.New(engine.Socket)
	c.home = filepath.Join(srv.root, "home/fly")
	c.walker.walk = func(context.Context, string) (uint64, int, error) { return 4096, 0, nil }
	return c
}

// home is the folder of a project in the home folder of the fake server.
func (s *server) home(name string) string {
	return filepath.Join(s.root, "home/fly", name)
}

func TestSites(t *testing.T) {
	srv := newServer(t)
	// Four CPUs.
	srv.write("proc/stat", "cpu  1000 0 0 800 0 0 0 0 0 0\ncpu0 1 2 3 4 5 6 7 8 0 0\ncpu1 1 2 3 4 5 6 7 8 0 0\ncpu2 1 2 3 4 5 6 7 8 0 0\ncpu3 1 2 3 4 5 6 7 8 0 0\n")
	cs := []fakeContainer{
		{"a1", srv.home("example.com")},
		{"a2", srv.home("example.com")},
		{"b1", srv.home(".fly")},
		{"c1", "/srv/other"},                        // not in the home folder
		{"d1", ""},                                  // not a Compose container
		{"e1", srv.home("example.com/nested/deep")}, // the parent is not the home folder
	}
	srv.cgroup("a1", 1000000, 300<<20, 100<<20)
	srv.cgroup("a2", 2000000, 50<<20, 80<<20) // more inactive file than current: 0
	srv.cgroup("b1", 3000000, 400<<20, 0)
	srv.cgroup("c1", 1, 1, 0)
	srv.cgroup("e1", 1, 1, 0)
	c := sitesCollector(t, srv, cs)
	base := time.Now().Add(time.Second)

	first, err := c.Sample(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := siteNames(first.Sites); got != "[.fly example.com]" {
		t.Fatalf("sites = %s, want [.fly example.com]", got)
	}
	for _, site := range first.Sites {
		if site.CPUPercent != nil {
			t.Errorf("%s cpu = %v in the first sample, want null", site.Directory, *site.CPUPercent)
		}
	}
	if m := first.Sites[1].MemoryUsedBytes; m == nil || *m != 200<<20 {
		t.Errorf("example.com memory = %v, want %d", ptr(m), 200<<20)
	}

	// One minute: a1 and a2 use 2.4 s of CPU, b1 uses 4.8 s. The server has
	// 240 s of CPU in one minute.
	srv.cgroup("a1", 1000000+1200000, 300<<20, 100<<20)
	srv.cgroup("a2", 2000000+1200000, 50<<20, 80<<20)
	srv.cgroup("b1", 3000000+4800000, 400<<20, 0)
	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{".fly": 2, "example.com": 1}
	for _, site := range s.Sites {
		if site.CPUPercent == nil || !near(*site.CPUPercent, want[site.Directory]) {
			t.Errorf("%s cpu = %v, want %v", site.Directory, ptr(site.CPUPercent), want[site.Directory])
		}
	}
	if m := s.Sites[0].MemoryUsedBytes; m == nil || *m != 400<<20 {
		t.Errorf(".fly memory = %v, want %d", ptr(m), 400<<20)
	}
}

func TestSitesLeaveOutAContainerThatRestarted(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1000000, 1, 0)
	srv.cgroup("a2", 1000000, 1, 0)
	engineCS := []fakeContainer{{"a1", srv.home("example.com")}, {"a2", srv.home("example.com")}}
	c := sitesCollector(t, srv, engineCS)
	base := time.Now().Add(time.Second)
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}

	// a2 restarted: it has a new id, and its CPU time starts again from 0.
	srv.cgroup("a1", 1000000+600000, 1, 0)
	srv.cgroup("a3", 50000000, 1, 0)
	c.docker = dockerapi.New(testutil.NewEngine(t, testutil.EngineHandler("29.7.1",
		containerJSON([]fakeContainer{{"a1", srv.home("example.com")}, {"a3", srv.home("example.com")}}))).Socket)

	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sites) != 1 || s.Sites[0].CPUPercent == nil || !near(*s.Sites[0].CPUPercent, 1) {
		t.Errorf("sites = %+v, want example.com with 1%% from a1 only", s.Sites)
	}
}

func TestSitesWithTheCgroupfsDriver(t *testing.T) {
	srv := newServer(t)
	srv.cgroupAt("sys/fs/cgroup/docker/a1", 1000000, 10<<20, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("example.com")}})

	s, err := c.Sample(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Sites) != 1 || s.Sites[0].MemoryUsedBytes == nil || *s.Sites[0].MemoryUsedBytes != 10<<20 {
		t.Errorf("sites = %+v, want the memory of a1", s.Sites)
	}
}

func TestSitesNullAndEmpty(t *testing.T) {
	t.Run("no project", func(t *testing.T) {
		srv := newServer(t)
		srv.cgroup("c1", 1, 1, 0)
		c := sitesCollector(t, srv, []fakeContainer{{"c1", "/srv/other"}})
		s, err := c.Sample(time.Now().Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"sites":[]`) {
			t.Errorf("sample JSON = %s, want \"sites\":[]: Docker runs, and no project matches", data)
		}
	})

	for _, tt := range []struct {
		name  string
		setup func(*testing.T, *server, *Collector)
	}{
		{"Docker does not run", func(_ *testing.T, _ *server, c *Collector) {
			c.docker = dockerapi.New("/nonexistent/docker.sock")
		}},
		{"cgroup v1", func(t *testing.T, s *server, _ *Collector) {
			if err := os.Remove(filepath.Join(s.root, "sys/fs/cgroup/cgroup.controllers")); err != nil {
				t.Fatal(err)
			}
		}},
		{"no home folder", func(_ *testing.T, _ *server, c *Collector) { c.home = "" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t)
			srv.cgroup("a1", 1, 1, 0)
			c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("example.com")}})
			tt.setup(t, srv, c)
			s, err := c.Sample(time.Now().Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"sites":null`) {
				t.Errorf("sample JSON = %s, want \"sites\":null", data)
			}
		})
	}
}

func TestSitesDiskIsSentOneTime(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("example.com")}})
	var walked []string
	c.walker.walk = func(_ context.Context, root string) (uint64, int, error) {
		walked = append(walked, root)
		return 8192, 0, nil
	}
	base := time.Now().Add(time.Second)

	first, err := c.Sample(base)
	if err != nil {
		t.Fatal(err)
	}
	if first.Sites[0].DiskUsedBytes != nil {
		t.Errorf("disk in the first sample = %d, want null: the walk runs in the background", *first.Sites[0].DiskUsedBytes)
	}
	waitWalk(t, c)
	if len(walked) != 1 || walked[0] != srv.home("example.com") {
		t.Errorf("walked %v, want the folder of example.com", walked)
	}

	second, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := second.Sites[0].DiskUsedBytes; d == nil || *d != 8192 {
		t.Errorf("disk in the second sample = %v, want 8192", ptr(d))
	}

	// One time only, and no new walk within one hour.
	third, err := c.Sample(base.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	waitWalk(t, c)
	if third.Sites[0].DiskUsedBytes != nil || len(walked) != 1 {
		t.Errorf("third sample disk = %v after %d walks, want null after 1 walk", ptr(third.Sites[0].DiskUsedBytes), len(walked))
	}
}

func TestSitesDiskWalkThatFails(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("example.com")}})
	c.walker.walk = func(context.Context, string) (uint64, int, error) { return 0, 0, context.DeadlineExceeded }

	if _, err := c.Sample(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	waitWalk(t, c)
	s, err := c.Sample(time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.Sites[0].DiskUsedBytes != nil {
		t.Errorf("disk = %d, want null after a walk that stopped", *s.Sites[0].DiskUsedBytes)
	}
}

func TestSitesLeaveOutAContainerThatRestartedWithTheSameID(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1000000, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("example.com")}})
	base := time.Now().Add(time.Second)
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}

	// docker restart keeps the id, but makes a new cgroup whose CPU time
	// starts again. The new run already used more than the old one.
	scope := filepath.Join(srv.root, "sys/fs/cgroup/system.slice/docker-a1.scope")
	if err := os.Rename(scope, scope+".old"); err != nil {
		t.Fatal(err)
	}
	srv.cgroup("a1", 1600000, 1, 0)

	s, err := c.Sample(base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.Sites[0].CPUPercent != nil {
		t.Errorf("cpu = %v, want null: the container restarted in the minute", *s.Sites[0].CPUPercent)
	}
}

func TestSitesWithTheRootAsHome(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", "/"}, {"a2", "/srv"}})
	c.home = "/"
	s, err := c.Sample(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if s.Sites != nil {
		t.Errorf("sites = %+v, want null: with / as the home, no folder is a site", s.Sites)
	}
}

func TestSitesDiskResultSurvivesASampleThatFails(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("example.com")}})
	base := time.Now().Add(time.Second)
	if _, err := c.Sample(base); err != nil {
		t.Fatal(err)
	}
	waitWalk(t, c)

	// The tick that would carry the disk use fails.
	statfs := c.statfs
	c.statfs = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	if _, err := c.Sample(base.Add(time.Minute)); err == nil {
		t.Fatal("Sample() = nil error, want the statfs error")
	}
	c.statfs = statfs

	s, err := c.Sample(base.Add(2 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if d := s.Sites[0].DiskUsedBytes; d == nil || *d != 4096 {
		t.Errorf("disk = %v, want 4096 in the next sample that goes", ptr(d))
	}
}

func TestSitesDiskWalkThatHangs(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1, 1, 0)
	srv.cgroup("b1", 1, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home("a.com")}, {"b1", srv.home("b.com")}})
	// The walk of a.com hangs in a system call and does not see its
	// context. The walk of b.com works.
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	c.walker.timeout = 50 * time.Millisecond
	c.walker.walk = func(_ context.Context, root string) (uint64, int, error) {
		if strings.HasSuffix(root, "a.com") {
			<-hang
		}
		return 4096, 0, nil
	}

	if _, err := c.Sample(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	waitWalk(t, c)
	s, err := c.Sample(time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if s.Sites[0].DiskUsedBytes != nil || s.Sites[1].DiskUsedBytes == nil {
		t.Errorf("disk = %v, %v; want null for a.com and 4096 for b.com", ptr(s.Sites[0].DiskUsedBytes), ptr(s.Sites[1].DiskUsedBytes))
	}
	c.walker.mu.Lock()
	running := c.walker.running
	c.walker.mu.Unlock()
	if running {
		t.Error("the walk still runs: a walk that hangs must not stop the next walks")
	}
}

func TestSitesWarnOneTimeForFilesThatCannotBeRead(t *testing.T) {
	srv := newServer(t)
	srv.cgroup("a1", 1, 1, 0)
	c := sitesCollector(t, srv, []fakeContainer{{"a1", srv.home(".fly")}})
	rec := &levels{}
	c.log = slog.New(rec)
	c.walker.walk = func(context.Context, string) (uint64, int, error) { return 4096, 9, nil }

	for i := range 3 {
		c.walker.mu.Lock()
		c.walker.last = time.Time{} // the hour passed
		c.walker.mu.Unlock()
		if _, err := c.Sample(time.Now().Add(time.Duration(i+1) * time.Minute)); err != nil {
			t.Fatal(err)
		}
		waitWalk(t, c)
	}

	msg := "some files of a site cannot be read; its disk use is lower than the real use"
	if got := rec.count(slog.LevelWarn, msg); got != 1 {
		t.Errorf("%d warnings for 3 walks, want 1", got)
	}
	if got := rec.count(slog.LevelDebug, msg); got != 2 {
		t.Errorf("%d debug lines for 3 walks, want 2", got)
	}
}

// levels is a slog handler that keeps the level and the message of each
// record.
type levels struct {
	mu      sync.Mutex
	records []slog.Record
}

func (l *levels) Enabled(context.Context, slog.Level) bool { return true }
func (l *levels) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *levels) WithGroup(string) slog.Handler            { return l }

func (l *levels) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, r)
	return nil
}

func (l *levels) count(level slog.Level, msg string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, r := range l.records {
		if r.Level == level && r.Message == msg {
			n++
		}
	}
	return n
}

// waitWalk waits until the disk walk that runs ends.
func waitWalk(t *testing.T, c *Collector) {
	t.Helper()
	c.walker.mu.Lock()
	done := c.walker.done
	c.walker.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the disk walk did not end")
	}
}

func siteNames(sites []wire.Site) string {
	var names []string
	for _, s := range sites {
		names = append(names, s.Directory)
	}
	return fmt.Sprint(names)
}
