package docker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/testutil"
)

// useFakeDocker puts a fake docker in PATH that behaves as mode describes.
func useFakeDocker(t *testing.T, mode string) *testutil.FakeDocker {
	t.Helper()

	fake := testutil.NewFakeDocker(t)
	fake.Install(t)
	t.Setenv(testutil.EnvMode, mode)

	return fake
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		noDocker   bool
		wantPart   Part
		wantDetail string
	}{
		{name: "available"},
		{name: "no docker CLI", noDocker: true, wantPart: PartCLI, wantDetail: "docker not found in PATH"},
		{name: "no compose plugin", mode: "no-compose", wantPart: PartCompose, wantDetail: "'compose' is not a docker command"},
		{name: "daemon down", mode: "daemon-down", wantPart: PartDaemon, wantDetail: "Cannot connect to the Docker daemon"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.noDocker {
				t.Setenv("PATH", t.TempDir())
			} else {
				useFakeDocker(t, tt.mode)
			}

			err := Check(context.Background())
			if tt.wantPart == "" {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}

			var unavailable *UnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("Check() = %v, want an *UnavailableError", err)
			}
			if unavailable.Part != tt.wantPart || !strings.Contains(unavailable.Detail, tt.wantDetail) {
				t.Errorf("Check() = %q, want part %q with detail %q", unavailable, tt.wantPart, tt.wantDetail)
			}
			if unavailable.ExitCode() != ExitUnavailable {
				t.Errorf("ExitCode() = %d, want %d", unavailable.ExitCode(), ExitUnavailable)
			}
		})
	}
}

func TestStatus(t *testing.T) {
	fake := useFakeDocker(t, "")

	got := Status(context.Background())
	want := []PartStatus{
		{Part: PartCLI, Info: filepath.Join(fake.Dir, "docker")},
		{Part: PartCompose, Info: "2.40.0"},
		{Part: PartDaemon, Info: "29.0.0"},
	}

	if len(got) != len(want) {
		t.Fatalf("Status() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i].Part != want[i].Part || got[i].Info != want[i].Info || got[i].Err != nil {
			t.Errorf("Status()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStatusWithoutDockerCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	for _, s := range Status(context.Background()) {
		if s.Err == nil {
			t.Errorf("%s: Err = nil, want not available", s.Part)
		}
	}
}

func TestCheckTimesOut(t *testing.T) {
	useFakeDocker(t, "daemon-hang")

	old := probeTimeout
	probeTimeout = time.Second
	t.Cleanup(func() { probeTimeout = old })

	start := time.Now()
	err := Check(context.Background())

	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || unavailable.Part != PartDaemon || !strings.Contains(unavailable.Detail, "no answer") {
		t.Errorf("Check() = %v, want the daemon to be reported as not answering", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Check() took %s, want it to stop soon after the timeout", elapsed)
	}
}
