package cmd

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flywp/server-cli/internal/testutil"
)

func TestForEachSiteContinuesAfterFailure(t *testing.T) {
	fake := testutil.NewFakeDocker(t)
	fake.Install(t)
	t.Setenv(testutil.EnvExit, "3")

	root := testutil.TempDir(t)
	testutil.WriteSite(t, filepath.Join(root, "a.example.com"), "php")
	testutil.WriteSite(t, filepath.Join(root, "b.example.com"), "php")
	testutil.WriteSite(t, filepath.Join(root, ".fly"), "mysql") // hidden: skipped

	old := sitesDir
	sitesDir = root
	t.Cleanup(func() { sitesDir = old })

	err := forEachSite("Starting", "up", "-d")
	if err == nil {
		t.Fatal("forEachSite() = nil, want an error for the failed sites")
	}

	// The summary must be printed, so it must not unwrap to the child's exit status.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		t.Errorf("forEachSite() error unwraps to *exec.ExitError, want a plain summary: %v", err)
	}
	for _, site := range []string{"a.example.com", "b.example.com"} {
		if !strings.Contains(err.Error(), "starting "+site) {
			t.Errorf("error %q does not name the phase and site %s", err, site)
		}
	}

	if calls := fake.Calls(t); len(calls) != 2 {
		t.Errorf("docker calls = %q, want one call per site", calls)
	}
}

func TestSitesRestartNamesThePhaseOfEachFailure(t *testing.T) {
	fake := testutil.NewFakeDocker(t)
	fake.Install(t)
	t.Setenv(testutil.EnvExit, "3")

	root := testutil.TempDir(t)
	testutil.WriteSite(t, filepath.Join(root, "broken.example.com"), "php")

	old := sitesDir
	sitesDir = root
	t.Cleanup(func() { sitesDir = old })

	// The site fails to stop and then fails to start: two different failures.
	err := restartSitesCmd.RunE(restartSitesCmd, nil)
	if err == nil {
		t.Fatal("sites restart = nil, want an error for the failed site")
	}

	got := strings.Split(err.Error(), "\n")
	want := []string{
		"stopping broken.example.com: exit status 3",
		"starting broken.example.com: exit status 3",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("error lines = %q, want %q", got, want)
	}
}

func TestForEachSiteMissingDirectory(t *testing.T) {
	old := sitesDir
	sitesDir = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { sitesDir = old })

	if err := forEachSite("Starting", "up", "-d"); err == nil {
		t.Fatal("forEachSite() = nil, want an error for a missing sites directory")
	}
}
