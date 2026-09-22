package agent

import (
	"strings"
	"testing"
)

func TestLockAllowsOneAgent(t *testing.T) {
	dir := t.TempDir()

	unlock, err := lock(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lock(dir); err == nil || !strings.Contains(err.Error(), "a different agent is running") {
		t.Fatalf("second lock() error = %v, want a different agent is running", err)
	}

	unlock()

	unlock, err = lock(dir)
	if err != nil {
		t.Fatalf("lock() after unlock error = %v", err)
	}
	unlock()
}
