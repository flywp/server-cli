package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// lock takes an exclusive lock on the lock file in dir, so that only one agent
// uses the state files. The lock ends when unlock runs or the process exits.
func lock(dir string) (unlock func(), err error) {
	path := filepath.Join(dir, "lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("a different agent is running: %s is locked", path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	// Closing the file releases the lock.
	return func() { _ = f.Close() }, nil
}
