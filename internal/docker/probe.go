package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ExitUnavailable is the exit status of a command that needs Docker when
// Docker is not available (EX_UNAVAILABLE in sysexits.h).
const ExitUnavailable = 69

// probeTimeout limits each availability check.
var probeTimeout = 5 * time.Second

// Part is a part of the Docker installation that fly needs.
type Part string

const (
	PartCLI     Part = "Docker CLI"
	PartCompose Part = "Docker Compose plugin"
	PartDaemon  Part = "Docker daemon"
)

// UnavailableError reports that a part of Docker is not available.
type UnavailableError struct {
	Part   Part
	Detail string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%s is not available: %s", e.Part, e.Detail)
}

// ExitCode returns the exit status for a command that cannot run without Docker.
func (e *UnavailableError) ExitCode() int {
	return ExitUnavailable
}

// PartStatus is the state of one part of Docker. Err is nil when the part is
// available; Info then holds its path or version.
type PartStatus struct {
	Part Part
	Info string
	Err  *UnavailableError
}

// Status checks the Docker CLI, the Compose plugin and the daemon, in that order.
func Status(ctx context.Context) []PartStatus {
	path, err := exec.LookPath("docker")
	if err != nil {
		const skipped = "not checked, because the Docker CLI is not available"
		return []PartStatus{
			{Part: PartCLI, Err: &UnavailableError{Part: PartCLI, Detail: "docker not found in PATH"}},
			{Part: PartCompose, Err: &UnavailableError{Part: PartCompose, Detail: skipped}},
			{Part: PartDaemon, Err: &UnavailableError{Part: PartDaemon, Detail: skipped}},
		}
	}

	return []PartStatus{
		{Part: PartCLI, Info: path},
		probe(ctx, PartCompose, "compose", "version", "--short"),
		probe(ctx, PartDaemon, "version", "--format", "{{.Server.Version}}"),
	}
}

// Check returns an *UnavailableError for the first part of Docker that is
// not available, or nil when Docker can run compose commands.
func Check(ctx context.Context) error {
	for _, s := range Status(ctx) {
		if s.Err != nil {
			return s.Err
		}
	}

	return nil
}

// probe runs docker with args and reports part as available if it succeeds.
func probe(ctx context.Context, part Part, args ...string) PartStatus {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Do not wait for output from processes that docker started, after a timeout.
	cmd.WaitDelay = time.Second

	if err := cmd.Run(); err != nil {
		detail := firstLine(stderr.String())
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			detail = fmt.Sprintf("no answer within %s", probeTimeout)
		case detail == "":
			detail = err.Error()
		}
		return PartStatus{Part: part, Err: &UnavailableError{Part: part, Detail: detail}}
	}

	return PartStatus{Part: part, Info: strings.TrimSpace(stdout.String())}
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}

	return ""
}
