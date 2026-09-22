package statefile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type state struct {
	Interval int      `json:"interval"`
	Names    []string `json:"names"`
}

func TestWriteThenRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	want := state{Interval: 5, Names: []string{"a", "b"}}
	if err := Write(path, want); err != nil {
		t.Fatal(err)
	}

	var got state
	if err := Read(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Interval != want.Interval || len(got.Names) != 2 {
		t.Errorf("Read() = %+v, want %+v", got, want)
	}

	// Only the file itself is left: no temporary files.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only state.json", len(entries))
	}
}

func TestWriteReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	for i := 1; i <= 3; i++ {
		if err := Write(path, state{Interval: i}); err != nil {
			t.Fatal(err)
		}
	}

	var got state
	if err := Read(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Interval != 3 {
		t.Errorf("Interval = %d, want 3", got.Interval)
	}
}

func TestReadMissing(t *testing.T) {
	got := state{Interval: 7}
	err := Read(filepath.Join(t.TempDir(), "missing.json"), &got)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read() error = %v, want fs.ErrNotExist", err)
	}
	if got.Interval != 7 {
		t.Errorf("Read() changed v to %+v", got)
	}
}

func TestReadIgnoresTornTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, state{Interval: 2}); err != nil {
		t.Fatal(err)
	}

	// A crash during a write leaves a partial temporary file next to the
	// complete one. Read must still see the complete file.
	if err := os.WriteFile(filepath.Join(dir, ".state.json.tmp-123"), []byte(`{"interv`), 0o600); err != nil {
		t.Fatal(err)
	}

	var got state
	if err := Read(path, &got); err != nil {
		t.Fatal(err)
	}
	if got.Interval != 2 {
		t.Errorf("Interval = %d, want 2", got.Interval)
	}
}

func TestReadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got state
	if err := Read(path, &got); err == nil {
		t.Fatal("Read() = nil, want an error for invalid JSON")
	}
}
