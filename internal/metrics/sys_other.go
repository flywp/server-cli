//go:build !linux

package metrics

import (
	"errors"
	"runtime"
)

// The agent measures only Linux servers. On other systems, for example a
// developer's Mac, the disk and the kernel are not measured.

func statfs(string) (total, used uint64, err error) {
	return 0, 0, errors.New("disk measurement is not supported on " + runtime.GOOS)
}

func kernelRelease() string {
	return ""
}

func lowerIOPriority() {}
