package main

// End-to-end tests of "fly agent run": the configuration errors, the lock, a
// clean stop and a real self-update. The loop itself is tested in
// internal/agent with a fake clock.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/release"
)

const testToken = "flyagt_0123456789abcdefghijABCDEFGHIJ"

// agentEnv is the environment of a valid agent with its own state directory.
// PATH holds no docker command: the agent must not need Docker.
func agentEnv(t *testing.T) []string {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("fly refuses to run as root")
	}

	return []string{
		"PATH=" + t.TempDir(),
		"HOME=" + t.TempDir(),
		"FLY_AGENT_URL=http://127.0.0.1:9",
		"FLY_AGENT_TOKEN=" + testToken,
		"FLY_AGENT_SERVER_ID=17",
		"STATE_DIRECTORY=" + t.TempDir(),
	}
}

// without returns environ without the variable key.
func without(environ []string, key string) []string {
	var out []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// lockedBuffer is a bytes.Buffer that a child process and the test can use
// at the same time.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startAgent starts "fly agent run" and waits until it logs that it started.
func startAgent(t *testing.T, environ []string) (*exec.Cmd, *lockedBuffer) {
	t.Helper()

	cmd := exec.Command(flyBin, "agent", "run")
	cmd.Env = environ
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(stderr.String(), "agent started") {
		if time.Now().After(deadline) {
			t.Fatalf("the agent did not start within 10s. stderr:\n%s", stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	return cmd, stderr
}

func TestAgentSendsAgentStartedToTheControlPlane(t *testing.T) {
	type request struct {
		path, auth string
		body       map[string][]map[string]any
	}
	got := make(chan request, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := request{path: r.URL.Path, auth: r.Header.Get("Authorization")}
		_ = json.NewDecoder(r.Body).Decode(&req.body)
		got <- req
		_, _ = w.Write([]byte(`{"accepted": 1}`))
	}))
	defer srv.Close()

	environ := append(agentEnv(t), "FLY_AGENT_URL="+srv.URL)
	cmd, stderr := startAgent(t, environ)
	defer func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() }()

	select {
	case req := <-got:
		if req.path != "/agent/v1/events" || req.auth != "Bearer "+testToken {
			t.Errorf("request to %s with Authorization %q, want /agent/v1/events with the token", req.path, req.auth)
		}
		if events := req.body["events"]; len(events) != 1 || events[0]["name"] != "agent.started" {
			t.Errorf("events = %v, want agent.started", events)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the control plane got no request within 10s. stderr:\n%s", stderr)
	}
}

// updateServer is a control plane with one open agent.update command. The
// command stays open until an event finishes it.
type updateServer struct {
	mu     sync.Mutex
	args   map[string]string
	events []map[string]any
	closed bool
}

func (s *updateServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.URL.Path {
	case "/agent/v1/events":
		var body struct{ Events []map[string]any }
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, e := range body.Events {
			s.events = append(s.events, e)
			if e["command_id"] == "01JBX0000000000000000000E1" {
				s.closed = true
			}
		}
		_, _ = w.Write([]byte(`{"accepted": 1}`))
	case "/agent/v1/commands":
		commands := []any{}
		if !s.closed {
			commands = append(commands, map[string]any{"id": "01JBX0000000000000000000E1", "verb": "agent.update", "args": s.args, "issued_at": "2026-09-22T10:00:00Z"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"commands": commands})
	default:
		_, _ = w.Write([]byte(`{"accepted": 1, "rejected": [], "report_interval": 1}`))
	}
}

// result returns the result event of the update command, or nil.
func (s *updateServer) result() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e["command_id"] == "01JBX0000000000000000000E1" {
			return e
		}
	}
	return nil
}

func TestAgentUpdatesItself(t *testing.T) {
	environ := agentEnv(t)

	// The agent replaces its own binary, so it runs from a copy.
	dir := t.TempDir()
	exe := filepath.Join(dir, "fly")
	data, err := os.ReadFile(flyBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}

	// The new release: fly built as v9.9.9, in the archive layout of a release.
	newBin := filepath.Join(t.TempDir(), "fly")
	build := exec.Command("go", "build", "-ldflags", "-X github.com/flywp/server-cli/internal/version.Version=v9.9.9", "-o", newBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the new release: %v\n%s", err, out)
	}
	tarball := releaseArchive(t, newBin, release.BinaryName(runtime.GOOS, runtime.GOARCH))
	sum := sha256.Sum256(tarball)

	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(tarball) }))
	defer files.Close()
	cp := &updateServer{args: map[string]string{"url": files.URL + "/fly.tar.gz", "version": "v9.9.9", "sha256": hex.EncodeToString(sum[:])}}
	srv := httptest.NewServer(cp)
	defer srv.Close()

	// Work 3 seconds from now, not at the second of server id 17.
	environ = append(environ, "FLY_AGENT_URL="+srv.URL, fmt.Sprintf("FLY_AGENT_SERVER_ID=%d", (time.Now().Second()+3)%60))

	first := exec.Command(exe, "agent", "run")
	first.Env = environ
	stderr := &lockedBuffer{}
	first.Stderr = stderr
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- first.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the agent exit after the update: %v, want 0. stderr:\n%s", err, stderr)
		}
	case <-time.After(30 * time.Second):
		_ = first.Process.Kill()
		t.Fatalf("the agent did not exit for the update within 30s. stderr:\n%s", stderr)
	}

	if out := runFlyAt(t, exe, "version"); !strings.Contains(out, "v9.9.9") {
		t.Fatalf("fly version = %q, want the new release v9.9.9 on disk", out)
	}
	if cp.result() != nil {
		t.Error("the old process sent the result; the new process must send it")
	}

	// systemd starts the new binary. It sends the result at once.
	second := exec.Command(exe, "agent", "run")
	second.Env = environ
	second.Stderr = &lockedBuffer{}
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Process.Signal(syscall.SIGTERM); _ = second.Wait() }()

	deadline := time.Now().Add(10 * time.Second)
	for cp.result() == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	e := cp.result()
	if e == nil || e["name"] != "command.completed" {
		t.Fatalf("result = %v, want command.completed", e)
	}
	if data, _ := e["data"].(map[string]any); data["version"] != "v9.9.9" {
		t.Errorf("result data = %v, want version v9.9.9", e["data"])
	}
}

// runFlyAt runs the fly binary at exe and returns its stdout.
func runFlyAt(t *testing.T, exe string, args ...string) string {
	t.Helper()
	out, err := exec.Command(exe, args...).Output()
	if err != nil {
		t.Fatalf("%s %v: %v", exe, args, err)
	}
	return string(out)
}

// releaseArchive returns a tar.gz archive that holds the file bin as name.
func releaseArchive(t *testing.T, bin, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.Bytes()
}

func TestAgentConfigErrors(t *testing.T) {
	tests := []struct {
		name    string
		environ func([]string) []string
		want    string
	}{
		{"no token", func(e []string) []string { return without(e, "FLY_AGENT_TOKEN") }, "FLY_AGENT_TOKEN is not set"},
		{"no state directory", func(e []string) []string { return without(e, "STATE_DIRECTORY") }, "STATE_DIRECTORY is not set"},
		{"plain http", func(e []string) []string { return append(e, "FLY_AGENT_URL=http://example.com") }, "must be an https URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := runFly(t, t.TempDir(), tt.environ(agentEnv(t)), "agent", "run")

			if res.code != 1 {
				t.Errorf("exit code = %d, want 1", res.code)
			}
			if !strings.Contains(res.stderr, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", res.stderr, tt.want)
			}
		})
	}
}

func TestAgentRejectsArguments(t *testing.T) {
	res := runFly(t, t.TempDir(), agentEnv(t), "agent", "run", "extra")
	if res.code != 1 || !strings.Contains(res.stderr, "unknown command") && !strings.Contains(res.stderr, "accepts 0 arg") {
		t.Errorf("fly agent run extra: exit %d, stderr %q; want a usage error", res.code, res.stderr)
	}
}

func TestAgentRunsWithoutDockerAndStopsOnSIGTERM(t *testing.T) {
	environ := agentEnv(t)
	cmd, stderr := startAgent(t, environ)

	// Only one agent can use the state directory.
	second := runFly(t, t.TempDir(), environ, "agent", "run")
	if second.code != 1 || !strings.Contains(second.stderr, "a different agent is running") {
		t.Errorf("second agent: exit %d, stderr %q; want exit 1 and a lock error", second.code, second.stderr)
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("agent exit after SIGTERM: %v, want exit status 0. stderr:\n%s", err, stderr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the agent did not stop within 10s after SIGTERM")
	}

	out := stderr.String()
	if !strings.Contains(out, "agent stopped") {
		t.Errorf("stderr = %q, want an \"agent stopped\" line", out)
	}
	if strings.Contains(out, testToken) {
		t.Error("the agent log contains the token")
	}
}
