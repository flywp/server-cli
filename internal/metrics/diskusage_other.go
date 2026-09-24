//go:build !unix

package metrics

import (
	"context"
	"errors"
	"runtime"
)

func diskUsage(context.Context, string) (uint64, int, error) {
	return 0, 0, errors.New("disk use is not measured on " + runtime.GOOS)
}
