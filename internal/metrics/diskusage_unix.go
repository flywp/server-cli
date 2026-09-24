//go:build unix

package metrics

import (
	"context"
	"io/fs"
	"path/filepath"
	"syscall"
)

// diskUsage returns the space that the files under root take on the disk: the
// allocated blocks, as du shows them (st_blocks × 512). It does not follow a
// symbolic link, does not go into an other file system, and counts a file
// with more than one hard link one time. It skips what it cannot read, and
// returns the number of those skips. It stops when ctx is done.
func diskUsage(ctx context.Context, root string) (used uint64, skipped int, err error) {
	var dev uint64
	seen := map[[2]uint64]bool{}

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

		used += uint64(st.Blocks) * 512
		return nil
	})

	return used, skipped, err
}
