package main

// End-to-end tests of "fly agent run": the configuration errors, the lock and
// a clean stop. The loop itself is tested in internal/agent with a fake clock.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
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
