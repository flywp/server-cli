package dockerapi

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/testutil"
)

var engineHandler = testutil.EngineHandler("29.7.1",
	`[{"Id":"abc","Names":["/x"],"Labels":{"com.docker.compose.project.working_dir":"/home/fly/example.com"}},{"Id":"def","Labels":{}}]`)

func TestVersionAndContainers(t *testing.T) {
	e := testutil.NewEngine(t, engineHandler)
	c := New(e.Socket)

	v, err := c.Version(context.Background())
	if err != nil || v != "29.7.1" {
		t.Errorf("Version() = %q, %v; want 29.7.1", v, err)
	}
	cs, err := c.Containers(context.Background())
	if err != nil || len(cs) != 2 || cs[0].ID != "abc" || cs[0].Labels["com.docker.compose.project.working_dir"] != "/home/fly/example.com" {
		t.Errorf("Containers() = %+v, %v", cs, err)
	}
	if got := e.Requests(); len(got) != 2 || got[0] != "GET /version" || got[1] != "GET /containers/json" {
		t.Errorf("requests = %v, want only GET /version and GET /containers/json", got)
	}
}

func TestErrorAnswer(t *testing.T) {
	e := testutil.NewEngine(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if _, err := New(e.Socket).Version(context.Background()); err == nil {
		t.Error("Version() = nil error, want the 500")
	}
}

func TestTimeout(t *testing.T) {
	release := make(chan struct{})
	e := testutil.NewEngine(t, func(http.ResponseWriter, *http.Request) { <-release })
	defer close(release)

	c := New(e.Socket)
	c.Timeout = 50 * time.Millisecond
	start := time.Now()
	_, err := c.Version(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Version() = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Version() took %v, want the timeout", d)
	}
}
