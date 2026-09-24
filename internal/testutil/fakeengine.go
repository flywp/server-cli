package testutil

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Engine is a fake Docker Engine API on a unix socket. It records the
// requests.
type Engine struct {
	Socket string

	mu       sync.Mutex
	requests []string
}

// NewEngine starts a fake engine that answers with handler. The socket path is
// short: a unix socket path has at most 104 bytes on macOS.
func NewEngine(t *testing.T, handler http.HandlerFunc) *Engine {
	t.Helper()
	dir, err := os.MkdirTemp("", "dk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	e := &Engine{Socket: filepath.Join(dir, "docker.sock")}
	l, err := net.Listen("unix", e.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.requests = append(e.requests, r.Method+" "+r.URL.Path)
		e.mu.Unlock()
		handler(w, r)
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return e
}

// Requests returns the method and the path of each request.
func (e *Engine) Requests() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.requests...)
}

// EngineHandler answers GET /version with version, and GET /containers/json
// with containers (JSON).
func EngineHandler(version, containers string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"Platform":{"Name":"Docker Engine - Community"},"Version":"` + version + `","ApiVersion":"1.52"}`))
		case "/containers/json":
			_, _ = w.Write([]byte(containers))
		default:
			http.NotFound(w, r)
		}
	}
}
