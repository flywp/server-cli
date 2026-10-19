//go:build !unix

package metrics

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"runtime"
)

func fileID(string) uint64 { return 0 }

func openNoFollow(path string) (*os.File, error) { return os.OpenFile(path, os.O_RDWR, 0) }

func inodeOf(fs.FileInfo) uint64 { return 0 }

func diskUsage(context.Context, string) (folderUse, int, error) {
	return folderUse{}, 0, errors.New("disk use is not measured on " + runtime.GOOS)
}
