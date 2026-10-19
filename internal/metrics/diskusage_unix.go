//go:build unix

package metrics

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// openNoFollow opens a file to read and cut it, and refuses a symbolic link.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
}

// inodeOf returns the inode of an open file's info, or 0.
func inodeOf(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino)
	}
	return 0
}

// fileID returns the inode of a path, or 0.
func fileID(path string) uint64 {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0
	}
	return uint64(st.Ino)
}

// diskUsage returns the space that the files under root take on the disk: the
// allocated blocks, as du shows them (st_blocks × 512), and the part of it in
// the app folder of root, the site's own files (nil without one). It does not
// follow a symbolic link, does not go into an other file system, and counts a
// file with more than one hard link one time. It skips what it cannot read,
// and returns the number of those skips. It stops when ctx is done.
func diskUsage(ctx context.Context, root string) (use folderUse, skipped int, err error) {
	var dev, own uint64
	seen := map[[2]uint64]bool{}
	app := filepath.Join(root, "app")

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == root {
				return err
			}
			// A folder that cannot be read is skipped. Its own blocks
			// were counted before.
			skipped++
			return nil
		}

		info, err := d.Info()
		if err != nil {
			skipped++
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			skipped++
			return nil
		}

		if path == root {
			dev = uint64(st.Dev)
		} else if uint64(st.Dev) != dev {
			// A mount point of an other file system.
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && st.Nlink > 1 {
			key := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			if seen[key] {
				return nil
			}
			seen[key] = true
		}

		blocks := uint64(st.Blocks) * 512
		use.used += blocks
		if path == app && d.IsDir() {
			use.own = &own
		}
		if path == app || strings.HasPrefix(path, app+string(filepath.Separator)) {
			own += blocks
		}
		return nil
	})

	return use, skipped, err
}
