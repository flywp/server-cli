// Package service controls the systemd service of the monitoring agent,
// fly-agent.service, for "fly update".
package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	if errors.Is(err, fs.ErrNotExist) {
		// The process ended after systemctl showed it, for example for its
		// own update or restart. systemd starts the binary on the disk.
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading the binary of the agent: %w", err)
	}

	return strings.HasSuffix(exe, " (deleted)"), nil
}

// Restart restarts the agent if it runs, so that it runs the binary on the
// disk. An agent that an administrator stopped stays stopped.
func Restart(ctx context.Context) error {
	_, err := systemctl(ctx, "try-restart", unit)
	return err
}

// systemctlTimeout is longer than the default stop timeout of systemd (90 s).
const systemctlTimeout = 2 * time.Minute

func systemctl(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, systemctlTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		// %v, not %w: an *exec.ExitError would tell fly that the child
		// already showed its error, and the output here would be lost.
		return "", fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}
