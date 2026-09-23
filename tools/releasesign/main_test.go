package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/release"
)

func TestKeygenThenSign(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "release.key")

	var out bytes.Buffer
	if err := run([]string{"keygen", "-out", keyPath}, nil, &out); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, want 0600", info.Mode().Perm())
	}
	key, _ := os.ReadFile(keyPath)
	if strings.Contains(out.String(), "PRIVATE KEY") || strings.Contains(out.String(), string(key)) {
		t.Fatal("keygen printed the private key")
	}

	// The printed line parses, and it is the key of the private key file.
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	keys, err := release.ParseKeys(lines[len(lines)-1])
	if err != nil || len(keys) != 1 {
		t.Fatalf("public key line %q: %v, %v", lines[len(lines)-1], keys, err)
	}

	checksums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(checksums, []byte("abc  fly-linux-amd64.tar.gz\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The key comes from stdin, for example from a password manager.
	var sigFile bytes.Buffer
	if err := run([]string{"sign", "-key", "-", "-tag", "v0.2.1", checksums}, bytes.NewReader(key), &sigFile); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(checksums)
	if _, err := release.Verify(sigFile.Bytes(), data, "v0.2.1", keys, time.Now()); err != nil {
		t.Errorf("Verify() of the signed file = %v", err)
	}
}

func TestKeygenDoesNotOverwrite(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(keyPath, []byte("old key"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := run([]string{"keygen", "-out", keyPath}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("keygen over an existing file = nil error, want an error")
	}
	if data, _ := os.ReadFile(keyPath); string(data) != "old key" {
		t.Error("keygen changed an existing key file")
	}
}

func TestReadKeyRefusesText(t *testing.T) {
	if _, err := readKey("-", strings.NewReader("not a key")); err == nil {
		t.Error("readKey() of text = nil error, want an error")
	}
}
