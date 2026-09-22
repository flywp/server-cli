package main

// End-to-end tests: build the fly binary once and run it against a fake
// docker command, so that exit codes and output are the ones users see.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/testutil"
)

var flyBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fly-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	flyBin = filepath.Join(dir, "fly")
	if out, err := exec.Command("go", "build", "-o", flyBin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fly: %v\n%s", err, out)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	code           int
	stdout, stderr string
}

// env is a test environment: a home directory with one site in it and a
// fake docker command.
type env struct {
	home, site string
	docker     *testutil.FakeDocker
	vars       []string
}

func newEnv(t *testing.T, services ...string) *env {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("fly refuses to run as root")
	}
	if len(services) == 0 {
		services = []string{"php", "nginx"}
	}

	home := testutil.TempDir(t)
	site := filepath.Join(home, "example.com")
	testutil.WriteSite(t, site, services...)

	return &env{home: home, site: site, docker: testutil.NewFakeDocker(t)}
}

// run executes fly in dir with the test environment. It kills fly and fails
// the test if fly does not finish within 10 seconds.
func (e *env) run(t *testing.T, dir string, args ...string) result {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, flyBin, args...)
	cmd.Dir = dir
	cmd.Env = append(append(e.docker.Env(), "HOME="+e.home), e.vars...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}

	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		t.Fatalf("fly %s did not finish within 10s", strings.Join(args, " "))
	case errors.As(err, &exitErr):
		res.code = exitErr.ExitCode()
	case err != nil:
		t.Fatalf("running fly %s: %v", strings.Join(args, " "), err)
	}

	return res
}

func TestChildExitStatusPassesThrough(t *testing.T) {
	e := newEnv(t)
	e.vars = append(e.vars, testutil.EnvExit+"=7")

	res := e.run(t, e.site, "exec", "--", "php", "sh", "-c", "exit 7")
	if res.code != 7 {
		t.Errorf("exit code = %d, want 7 (stderr %q)", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing: the child reports its own error", res.stderr)
	}
}

func TestDockerFailureExitsNonZero(t *testing.T) {
	commands := [][]string{
		{"start"},
		{"stop"},
		{"restart"},
		{"restart", "php"},
		{"wp", "--", "plugin", "list"},
		{"exec", "--", "php", "ls"},
		{"logs"},
		{"base", "start"},
		{"base", "stop"},
		{"base", "restart"},
	}

	for _, args := range commands {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newEnv(t)
			e.vars = append(e.vars, testutil.EnvExit+"=3")

			res := e.run(t, e.site, args...)
			if res.code != 3 {
				t.Errorf("exit code = %d, want 3 (stderr %q)", res.code, res.stderr)
			}
			if strings.Contains(res.stdout, "successfully") {
				t.Errorf("stdout = %q, want no success message after a failure", res.stdout)
			}
			if strings.Contains(res.stdout+res.stderr, "%!") {
				t.Errorf("output has a format error: stdout %q, stderr %q", res.stdout, res.stderr)
			}
		})
	}
}

func TestSuccessExitsZero(t *testing.T) {
	e := newEnv(t)

	res := e.run(t, e.site, "start")
	if res.code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr %q)", res.code, res.stderr)
	}

	want := "compose -f " + filepath.Join(e.site, "docker-compose.yml") + " up -d"
	if calls := e.docker.Calls(t); len(calls) != 1 || calls[0] != want {
		t.Errorf("docker calls = %q, want [%q]", calls, want)
	}
}

func TestNoSiteIsAnError(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.home, "not-a-site")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	res := e.run(t, dir, "start")
	if res.code != 1 {
		t.Errorf("exit code = %d, want 1", res.code)
	}
	if !strings.Contains(res.stderr, "no docker-compose.yml file found") {
		t.Errorf("stderr = %q, want the no-site error", res.stderr)
	}
	if res.stdout != "" {
		t.Errorf("stdout = %q, want errors on stderr only", res.stdout)
	}
}

func TestOutsideHomeDoesNotHang(t *testing.T) {
	e := newEnv(t)
	outside := testutil.TempDir(t) // not inside e.home

	// run fails the test if fly does not stop.
	res := e.run(t, outside, "start")
	if res.code != 1 || !strings.Contains(res.stderr, "no docker-compose.yml file found") {
		t.Errorf("got exit %d, stderr %q, want exit 1 and the no-site error", res.code, res.stderr)
	}
}

func TestDomainFlag(t *testing.T) {
	e := newEnv(t)
	outside := testutil.TempDir(t)

	res := e.run(t, outside, "start", "--domain", "example.com")
	if res.code != 0 {
		t.Fatalf("--domain example.com: exit code = %d, want 0 (stderr %q)", res.code, res.stderr)
	}

	for _, bad := range []string{"../", "..", "example.com/../..", "a/b"} {
		res := e.run(t, outside, "start", "--domain", bad)
		if res.code != 1 || !strings.Contains(res.stderr, "invalid domain") {
			t.Errorf("--domain %q: exit %d, stderr %q, want exit 1 and an invalid domain error", bad, res.code, res.stderr)
		}
	}

	if calls := e.docker.Calls(t); len(calls) != 1 {
		t.Errorf("docker calls = %q, want only the call for example.com", calls)
	}
}

func TestFlagsPassThrough(t *testing.T) {
	tests := []struct {
		name     string
		services []string
		outside  bool // run outside the site directory
		args     []string
		want     string // docker arguments after "compose -f <file>"
	}{
		{
			name: "wp-cli flags",
			args: []string{"wp", "plugin", "list", "--format=json"},
			want: "exec -T php wp plugin list --format=json",
		},
		{
			name:    "domain before the wp-cli command",
			outside: true,
			args:    []string{"--domain", "example.com", "wp", "plugin", "list", "--format=json"},
			want:    "exec -T php wp plugin list --format=json",
		},
		{
			name: "exec flags",
			args: []string{"exec", "php", "ls", "-la"},
			want: "exec -T php ls -la",
		},
		{
			name: "exec in the default service",
			args: []string{"exec", "ls", "-la"},
			want: "exec -T php ls -la",
		},
		{
			name:     "exec on an OpenLiteSpeed site",
			services: []string{"openlitespeed"},
			args:     []string{"exec", "ls"},
			want:     "exec -T openlitespeed ls",
		},
		{
			name:     "wp on an OpenLiteSpeed site",
			services: []string{"openlitespeed"},
			args:     []string{"wp", "plugin", "list"},
			want:     "exec -T --user www-data openlitespeed wp plugin list",
		},
		{
			name: "follow logs of one service",
			args: []string{"logs", "-f", "php"},
			want: "logs --follow php",
		},
		{
			name: "tail logs",
			args: []string{"logs", "--tail", "50"},
			want: "logs --tail 50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, tt.services...)
			dir := e.site
			if tt.outside {
				dir = testutil.TempDir(t)
			}

			res := e.run(t, dir, tt.args...)
			if res.code != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr %q)", res.code, res.stderr)
			}

			want := "compose -f " + filepath.Join(e.site, "docker-compose.yml") + " " + tt.want
			if calls := e.docker.Calls(t); len(calls) != 1 || calls[0] != want {
				t.Errorf("docker calls = %q, want [%q]", calls, want)
			}
		})
	}
}

func TestExecNeedsACommand(t *testing.T) {
	e := newEnv(t)

	res := e.run(t, e.site, "exec", "php")
	if res.code != 1 || !strings.Contains(res.stderr, "no command given") {
		t.Errorf("exit %d, stderr %q, want exit 1 and a missing-command error", res.code, res.stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"start", "--bogus"}, want: "Run 'fly start --help' for usage"},
		{args: []string{"exec"}, want: "requires at least 1 arg"},
		{args: []string{"update"}, want: "sudo fly update"},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			e := newEnv(t)

			res := e.run(t, e.site, tt.args...)
			if res.code != 1 {
				t.Errorf("exit code = %d, want 1", res.code)
			}
			if !strings.Contains(res.stderr, tt.want) {
				t.Errorf("stderr = %q, want it to contain %q", res.stderr, tt.want)
			}
		})
	}
}
