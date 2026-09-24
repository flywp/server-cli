//go:build unix

package metrics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// blocks returns the allocated bytes of a path, as du counts them.
func blocks(t *testing.T, path string) uint64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Blocks) * 512
}

func TestDiskUsage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "example.com")
	outside := t.TempDir()
	for name, size := range map[string]int{"wp-config.php": 3000, "app/index.php": 100, "app/uploads/big.jpg": 200000} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A hard link counts one time. A symbolic link to a big file outside
	// the folder is not followed.
	if err := os.Link(filepath.Join(root, "app/uploads/big.jpg"), filepath.Join(root, "big-link.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "huge"), []byte(strings.Repeat("y", 1<<20)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "huge"), filepath.Join(root, "huge-link")); err != nil {
		t.Fatal(err)
	}

	var want uint64
	for _, p := range []string{"", "app", "app/uploads", "wp-config.php", "app/index.php", "app/uploads/big.jpg", "huge-link"} {
		want += blocks(t, filepath.Join(root, p))
	}

	got, skipped, err := diskUsage(context.Background(), root)
	if err != nil || skipped != 0 {
		t.Fatalf("diskUsage() = %d, %d skipped, %v", got, skipped, err)
	}
	if got != want {
		t.Errorf("diskUsage() = %d, want %d: the blocks of each file one time, without the target of the symbolic link", got, want)
	}
}

func TestDiskUsageSkipsWhatItCannotRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can read each folder")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "secret"), []byte(strings.Repeat("s", 100000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got, skipped, err := diskUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 1 || got != blocks(t, root)+blocks(t, locked) {
		t.Errorf("diskUsage() = %d with %d skipped, want the two folders and 1 skip", got, skipped)
	}
}

func TestDiskUsageStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := diskUsage(ctx, t.TempDir()); err == nil {
		t.Error("diskUsage() = nil error, want the error of the context")
	}
	if _, _, err := diskUsage(context.Background(), filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("diskUsage() = nil error, want an error for a folder that does not exist")
	}
}
