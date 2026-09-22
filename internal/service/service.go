// Package service controls the systemd service of the monitoring agent,
// fly-agent.service, for "fly update".
package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const unit = "fly-agent"

// UnitPath is the unit file that the FlyWP installer writes. procRoot is the
// proc file system. Tests replace them.
var (
	UnitPath = "/etc/systemd/system/fly-agent.service"
	procRoot = "/proc"
)

// Installed reports whether the server has the monitoring agent.
func Installed() bool {
	_, err := os.Stat(UnitPath)
	return err == nil
}

// Stale reports whether the agent runs a binary that was replaced: after a
// rename over the binary, the kernel shows the old one as "(deleted)".
func Stale(ctx context.Context) (bool, error) {
	out, err := systemctl(ctx, "show", "--property=MainPID", "--value", unit)
	if err != nil {
		return false, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return false, fmt.Errorf("reading the process id of %s: %q", unit, out)
	}
	if pid == 0 {
		// The agent does not run: systemd starts the binary on the disk.
		return false, nil
	}

	exe, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "exe"))
	if err != nil {
		return false, fmt.Errorf("reading the binary of the agent: %w", err)
	}

	return strings.HasSuffix(exe, " (deleted)"), nil
}

// Restart restarts the agent, so that it runs the binary on the disk.
func Restart(ctx context.Context) error {
	_, err := systemctl(ctx, "restart", unit)
	return err
}

func systemctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}
