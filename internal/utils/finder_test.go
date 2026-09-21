package utils

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/testutil"
)

// findWithTimeout fails the test if FindComposeFile does not return quickly.
func findWithTimeout(t *testing.T, domain string) (string, error) {
	t.Helper()

	type result struct {
		path string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		path, err := FindComposeFile(domain)
		done <- result{path, err}
	}()

	select {
	case r := <-done:
		return r.path, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("FindComposeFile did not return within 2s")
		return "", nil
	}
}

func TestFindComposeFileOutsideHomeStops(t *testing.T) {
	root := testutil.TempDir(t)
	t.Setenv("HOME", filepath.Join(root, "home"))

	outside := filepath.Join(root, "var", "www")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(outside)

	if _, err := findWithTimeout(t, ""); !errors.Is(err, ErrComposeNotFound) {
		t.Errorf("FindComposeFile() error = %v, want ErrComposeNotFound", err)
	}
}

func TestFindComposeFileFromNestedDirectory(t *testing.T) {
	home := testutil.TempDir(t)
	t.Setenv("HOME", home)

	want := testutil.WriteSite(t, filepath.Join(home, "example.com"), "php")
	nested := filepath.Join(home, "example.com", "app", "public", "wp-content")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	got, err := findWithTimeout(t, "")
	if err != nil || got != want {
		t.Errorf("FindComposeFile() = %q, %v, want %q, nil", got, err, want)
	}
}

func TestFindComposeFileStopsAtHome(t *testing.T) {
	root := testutil.TempDir(t)
	testutil.WriteSite(t, root, "php") // above the home directory: not a site
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "notes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	if got, err := findWithTimeout(t, ""); !errors.Is(err, ErrComposeNotFound) {
		t.Errorf("FindComposeFile() = %q, %v, want ErrComposeNotFound", got, err)
	}
}

func TestFindComposeFileWithDomain(t *testing.T) {
	home := testutil.TempDir(t)
	t.Setenv("HOME", home)
	want := testutil.WriteSite(t, filepath.Join(home, "example.com"), "php")

	if got, err := findWithTimeout(t, "example.com"); err != nil || got != want {
		t.Errorf("FindComposeFile(example.com) = %q, %v, want %q, nil", got, err, want)
	}

	if _, err := findWithTimeout(t, "other.com"); !errors.Is(err, ErrComposeNotFound) {
		t.Errorf("FindComposeFile(other.com) error = %v, want ErrComposeNotFound", err)
	}

	if _, err := findWithTimeout(t, "../"); err == nil || errors.Is(err, ErrComposeNotFound) {
		t.Errorf("FindComposeFile(../) error = %v, want an invalid domain error", err)
	}
}

func TestValidateDomain(t *testing.T) {
	valid := []string{
		"example.com",
		"www.example.co.uk",
		"my-site.example.com",
		"xn--mnchen-3ya.de",
		"localhost",
		strings.Repeat("a", 63) + ".com",
	}
	for _, d := range valid {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("ValidateDomain(%q) = %v, want nil", d, err)
		}
	}

	invalid := []string{
		"",
		".",
		"..",
		"../",
		"../etc",
		"a/b",
		"/etc",
		".example.com",
		"example..com",
		"example.com.",
		"-example.com",
		"example-.com",
		"exa mple.com",
		"exa_mple.com",
		strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 127) + "com",
	}
	for _, d := range invalid {
		if err := ValidateDomain(d); err == nil {
			t.Errorf("ValidateDomain(%q) = nil, want an error", d)
		}
	}
}
