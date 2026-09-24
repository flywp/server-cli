package metrics

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"syscall"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// dockerStatus returns the state and the version of Docker, from GET /version
// on the Docker socket (contract v0.5.0):
//
//   - an answer: running
//   - no socket, or the connection is refused: not_running when dockerd
//     exists, else not_installed
//   - any other result, for example "permission denied" or no answer in 5
//     seconds: nil, because the agent cannot tell
func (c *Collector) dockerStatus(ctx context.Context) (status, version *string) {
	v, err := c.docker.Version(ctx)
	switch {
	case err == nil:
		c.dockerWarned = false
		s := wire.DockerRunning
		if v == "" {
			return &s, nil
		}
		return &s, &v
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED):
		c.dockerWarned = false
		s := wire.DockerNotInstalled
		if _, err := os.Stat(c.file("usr/bin/dockerd")); err == nil {
			s = wire.DockerNotRunning
		}
		return &s, nil
	default:
		if !c.dockerWarned {
			c.dockerWarned = true
			c.log.Warn("cannot tell whether Docker runs; sending null", "error", err)
		}
		return nil, nil
	}
}
