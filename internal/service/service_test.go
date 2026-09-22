package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeSystemctl puts a systemctl command first in PATH. It logs its arguments
// and prints mainPID for "show".
func fakeSystemctl(t *testing.T, mainPID string, exit int) (log string) {
	t.Helper()

	dir := t.TempDir()
	log = filepath.Join(t.TempDir(), "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n" +
		"[ \"$1\" = show ] && echo " + mainPID + "\n" +
		"exit " + strconv.Itoa(exit) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return log
}

// fakeProc makes a proc directory in which process 4242 runs exe.
func fakeProc(t *testing.T, exe string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "4242"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(dir, "4242", "exe")); err != nil {
		t.Fatal(err)
	}

	old := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = old })
}

func calls(t *testing.T, log string) string {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

func TestInstalled(t *testing.T) {
	old := UnitPath
	t.Cleanup(func() { UnitPath = old })

	UnitPath = filepath.Join(t.TempDir(), "fly-agent.service")
	if Installed() {
		t.Error("Installed() = true without the unit file")
	}
	if err := os.WriteFile(UnitPath, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !Installed() {
		t.Error("Installed() = false with the unit file")
	}
}

func TestStale(t *testing.T) {
	tests := []struct {
		name, mainPID, exe string
		want               bool
	}{
		{"replaced binary", "4242", "/home/fly/.fly/bin/fly (deleted)", true},
		{"current binary", "4242", "/home/fly/.fly/bin/fly", false},
		{"agent not running", "0", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := fakeSystemctl(t, tt.mainPID, 0)
			if tt.exe != "" {
				fakeProc(t, tt.exe)
			}

			got, err := Stale(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("Stale() = %v, want %v", got, tt.want)
			}
			if c := calls(t, log); c != "show --property=MainPID --value fly-agent" {
				t.Errorf("systemctl calls = %q", c)
			}
		})
	}
}

func TestStaleErrors(t *testing.T) {
	fakeSystemctl(t, "not-a-number", 0)
	if _, err := Stale(context.Background()); err == nil {
		t.Error("Stale() = nil error, want an error for a bad process id")
	}

	fakeSystemctl(t, "4242", 1)
	if _, err := Stale(context.Background()); err == nil || !strings.Contains(err.Error(), "systemctl show") {
		t.Errorf("Stale() error = %v, want the systemctl error", err)
	}
}

func TestRestart(t *testing.T) {
	log := fakeSystemctl(t, "0", 0)
	if err := Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c := calls(t, log); c != "restart fly-agent" {
		t.Errorf("systemctl calls = %q, want restart fly-agent", c)
	}

	fakeSystemctl(t, "0", 1)
	if err := Restart(context.Background()); err == nil {
		t.Error("Restart() = nil error, want the systemctl error")
	}
}
