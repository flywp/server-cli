package metrics

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/dockerapi"
	"github.com/flywp/server-cli/internal/testutil"
)

// shortDir returns a folder with a short path, for a unix socket: its path
// has at most 104 bytes on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestCPUCount(t *testing.T) {
	srv := newServer(t)
	srv.write("proc/stat", "cpu  1 2 3 4 5 6 7 8 0 0\ncpu0 1 2 3 4 5 6 7 8 0 0\ncpu1 1 2 3 4 5 6 7 8 0 0\ncpu2 1 2 3 4 5 6 7 8 0 0\ncpu3 1 2 3 4 5 6 7 8 0 0\nintr 1 2 3\nctxt 5\ncpufreq 1\n")
	s := srv.collector(t.TempDir()).Status(context.Background())
	if s.CPUCount == nil || *s.CPUCount != 4 {
		t.Errorf("cpu_count = %v, want 4", ptr(s.CPUCount))
	}
}

func TestDockerStatus(t *testing.T) {
	engine := testutil.NewEngine(t, testutil.EngineHandler("29.7.1", "[]"))

	refused := filepath.Join(shortDir(t), "docker.sock")
	l, err := net.Listen("unix", refused)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		socket      string
		dockerd     bool
		wantStatus  any
		wantVersion any
	}{
		{"running", engine.Socket, true, wire.DockerRunning, "29.7.1"},
		{"no socket, with dockerd", "/nonexistent/docker.sock", true, wire.DockerNotRunning, "null"},
		{"no socket, without dockerd", "/nonexistent/docker.sock", false, wire.DockerNotInstalled, "null"},
		{"connection refused", refused, true, wire.DockerNotRunning, "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t)
			if tt.dockerd {
				srv.write("usr/bin/dockerd", "")
			}
			c := srv.collector(t.TempDir())
			c.docker = dockerapi.New(tt.socket)

			s := c.Status(context.Background())
			if ptr(s.DockerStatus) != tt.wantStatus || ptr(s.DockerVersion) != tt.wantVersion {
				t.Errorf("docker = %v %v, want %v %v", ptr(s.DockerStatus), ptr(s.DockerVersion), tt.wantStatus, tt.wantVersion)
			}
		})
	}
}

func TestDockerStatusIsNotKnown(t *testing.T) {
	t.Run("permission denied", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root can use any socket")
		}
		engine := testutil.NewEngine(t, testutil.EngineHandler("29.7.1", "[]"))
		if err := os.Chmod(engine.Socket, 0); err != nil {
			t.Fatal(err)
		}
		srv := newServer(t)
		srv.write("usr/bin/dockerd", "")
		c := srv.collector(t.TempDir())
		c.docker = dockerapi.New(engine.Socket)

		if s := c.Status(context.Background()); s.DockerStatus != nil || s.DockerVersion != nil {
			t.Errorf("docker = %v %v, want null: the agent cannot tell", ptr(s.DockerStatus), ptr(s.DockerVersion))
		}
	})

	t.Run("no answer", func(t *testing.T) {
		release := make(chan struct{})
		engine := testutil.NewEngine(t, func(http.ResponseWriter, *http.Request) { <-release })
		defer close(release)

		srv := newServer(t)
		c := srv.collector(t.TempDir())
		c.docker = dockerapi.New(engine.Socket)
		c.docker.Timeout = 50 * time.Millisecond

		s := c.Status(context.Background())
		if s.DockerStatus != nil || s.DockerVersion != nil {
			t.Errorf("docker = %v %v, want null", ptr(s.DockerStatus), ptr(s.DockerVersion))
		}
		// The other values are still there.
		if s.OS == "" || s.CPUCount == nil {
			t.Errorf("status = %+v, want the other values", s)
		}
	})
}

func TestDockerStatusSendsOnlyVersion(t *testing.T) {
	engine := testutil.NewEngine(t, testutil.EngineHandler("29.7.1", "[]"))
	srv := newServer(t)
	c := srv.collector(t.TempDir())
	c.docker = dockerapi.New(engine.Socket)

	c.Status(context.Background())
	if got := engine.Requests(); len(got) != 1 || got[0] != "GET /version" {
		t.Errorf("requests = %v, want only GET /version", got)
	}
}
