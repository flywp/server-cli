//go:build !unix

package metrics

import (
	"context"
	"errors"
	"runtime"
)

func fileID(string) uint64 { return 0 }

func diskUsage(context.Context, string) (uint64, int, error) {
	return 0, 0, errors.New("disk use is not measured on " + runtime.GOOS)
}
