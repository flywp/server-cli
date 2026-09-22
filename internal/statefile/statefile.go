// Package statefile keeps small JSON files that must survive a crash.
package statefile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// tempSuffix marks the temporary files of Write.
const tempSuffix = ".tmp-"

// Write stores v as JSON in path. It writes a temporary file in the same
// directory, syncs it and renames it over path, so path always holds a
// complete file, also after a crash or a power loss.
func Write(path string, v any) (err error) {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+tempSuffix+"*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}

	// Sync the directory too, so that the rename itself survives a power loss.
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

// Read decodes the JSON in path into v. When path does not exist, the error
// matches fs.ErrNotExist and v is not changed.
func Read(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	return nil
}

// RemoveTemp removes the temporary files that Write leaves in dir after a
// crash. Call it before any Write in dir starts.
func RemoveTemp(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, ".*"+tempSuffix+"*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
