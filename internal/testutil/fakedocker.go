// Package testutil provides helpers for tests that run fly against a fake
// docker command or a fake Docker Engine API instead of a real Docker
// installation.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Environment variables that control the fake docker command.
const (
	// EnvLog is the file that receives one line per docker call.
	EnvLog = "FAKE_DOCKER_LOG"
	// EnvExit is the exit status of "docker compose" calls (default 0).
	EnvExit = "FAKE_DOCKER_EXIT"
	// EnvMode selects a failure mode: "no-compose", "daemon-down" or
	// "daemon-hang".
	EnvMode = "FAKE_DOCKER_MODE"
)

const fakeDockerScript = `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
case "$FAKE_DOCKER_MODE" in
no-compose)
	if [ "$1" = compose ]; then
		echo "docker: 'compose' is not a docker command." >&2
		exit 1
	fi
	;;
daemon-hang)
	if [ "$1" = version ]; then
		exec sleep 30
	fi
	;;
daemon-down)
	if [ "$1 $2" != "compose version" ]; then
		echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?" >&2
		exit 1
	fi
	;;
esac
case "$1 $2" in
"compose version") echo "2.40.0"; exit 0 ;;
"version "*) echo "29.0.0"; exit 0 ;;
esac
exit "${FAKE_DOCKER_EXIT:-0}"
`

// FakeDocker is a fake docker command in its own directory.
type FakeDocker struct {
	// Dir holds the docker script. Put it first in PATH.
	Dir string
	// Log receives the arguments of each docker call, one call per line.
	Log string
}

// NewFakeDocker writes a fake docker command to a temporary directory.
func NewFakeDocker(t *testing.T) *FakeDocker {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}

	return &FakeDocker{Dir: dir, Log: filepath.Join(t.TempDir(), "docker.log")}
}

// Install puts the fake docker first in PATH and points it at its log for
// the duration of the test.
func (f *FakeDocker) Install(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", f.Dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(EnvLog, f.Log)
}

// Env returns the environment for a child process that uses the fake docker.
// The PATH holds only the fake docker and the system directories.
func (f *FakeDocker) Env() []string {
	return []string{
		"PATH=" + f.Dir + string(os.PathListSeparator) + "/usr/bin:/bin",
		EnvLog + "=" + f.Log,
	}
}

// Calls returns the arguments of each docker call so far.
func (f *FakeDocker) Calls(t *testing.T) []string {
	t.Helper()

	data, err := os.ReadFile(f.Log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}

	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// ComposeCalls returns the "docker compose -f" calls so far, without the
// calls that check whether Docker is available.
func (f *FakeDocker) ComposeCalls(t *testing.T) []string {
	t.Helper()

	var calls []string
	for _, c := range f.Calls(t) {
		if strings.HasPrefix(c, "compose -f ") {
			calls = append(calls, c)
		}
	}

	return calls
}

// WriteSite creates dir with a docker-compose.yml that defines services and
// returns the path of the compose file.
func WriteSite(t *testing.T, dir string, services ...string) string {
	t.Helper()

	var b strings.Builder
	b.WriteString("services:\n")
	for _, s := range services {
		fmt.Fprintf(&b, "  %s:\n    image: example\n", s)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	return composePath
}

// TempDir returns a new temporary directory with symlinks resolved, so that
// it compares equal to the working directory a child process sees.
func TempDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return dir
}
