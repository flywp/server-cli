package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flywp/server-cli/internal/service"
)

func TestConfirmNeedsATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	_, _ = w.WriteString("y\n")
	_ = w.Close()

	// A script that pipes "y" still needs --yes: the pipe is not a terminal.
	ok, err := confirm(r, io.Discard, "v0.2.0")
	if ok || err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("confirm() = %v, %v; want an error that tells to use --yes", ok, err)
	}
}

// fakeAgentService makes the agent unit exist and puts a fake systemctl in
// PATH. It returns the log of the systemctl calls.
func fakeAgentService(t *testing.T, installed bool) string {
	t.Helper()

	unit := filepath.Join(t.TempDir(), "fly-agent.service")
	if installed {
		if err := os.WriteFile(unit, []byte("[Unit]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := service.UnitPath
	service.UnitPath = unit
	t.Cleanup(func() { service.UnitPath = old })

	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n[ \"$1\" = show ] && echo 0\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return log
}

func systemctlCalls(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestRestartAgentAfterAnUpdate(t *testing.T) {
	log := fakeAgentService(t, true)
	if err := restartAgent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := systemctlCalls(t, log); got != "restart fly-agent" {
		t.Errorf("systemctl calls = %q, want restart fly-agent", got)
	}
}

func TestNoRestartWithoutTheAgent(t *testing.T) {
	log := fakeAgentService(t, false)
	if err := restartAgent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restartStaleAgent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := systemctlCalls(t, log); got != "" {
		t.Errorf("systemctl calls = %q, want none on a server without the agent", got)
	}
}

func TestNoRestartWhenTheAgentIsCurrent(t *testing.T) {
	// The fake systemctl shows MainPID 0: the agent does not run an old binary.
	log := fakeAgentService(t, true)
	if err := restartStaleAgent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := systemctlCalls(t, log); got != "show --property=MainPID --value fly-agent" {
		t.Errorf("systemctl calls = %q, want only the check", got)
	}
}
