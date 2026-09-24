package agent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/release"
	"github.com/flywp/server-cli/internal/statefile"
)

func TestUpdateSlot(t *testing.T) {
	tests := map[int64]time.Duration{
		0:    0,
		17:   17 * time.Minute,
		1439: 1439 * time.Minute,
		1445: 5 * time.Minute,
	}
	for id, want := range tests {
		if got := updateSlot(id); got != want {
			t.Errorf("updateSlot(%d) = %v, want %v", id, got, want)
		}
	}
}

func TestNextCheck(t *testing.T) {
	day := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	slot := 17 * time.Minute
	tests := []struct {
		name string
		last time.Time
		want time.Time
	}{
		{"never checked", time.Time{}, time.Time{}},
		{"checked at the slot", day.Add(slot), day.Add(24*time.Hour + slot)},
		{"checked late in the day", day.Add(20 * time.Hour), day.Add(48*time.Hour + slot)},
		{"checked just before the slot", day.Add(slot - time.Minute), day.Add(24*time.Hour + slot)},
		// Exactly 12 hours before a slot is not more than 12 hours.
		{"checked 12 hours before the slot", day.Add(slot - 12*time.Hour), day.Add(24*time.Hour + slot)},
		{"a zone that is not UTC", day.Add(slot).In(time.FixedZone("UTC+6", 6*3600)), day.Add(24*time.Hour + slot)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextCheck(tt.last, slot); !got.Equal(tt.want) {
				t.Errorf("nextCheck(%v) = %v, want %v", tt.last, got, tt.want)
			}
		})
	}
}

// fakeReleases replaces GitHub and the signature check for one test.
type fakeReleases struct {
	mu        sync.Mutex
	tag       string
	latestErr error
	signedAt  time.Time
	signErr   error

	checks []time.Time
	signed int
}

func useFakeReleases(t *testing.T, f *fakeReleases) {
	t.Helper()

	oldLatest, oldSigned, oldKeys, oldWritable := latestRelease, signedChecksum, releaseKeys, binaryDirWritable
	t.Cleanup(func() {
		latestRelease, signedChecksum, releaseKeys, binaryDirWritable = oldLatest, oldSigned, oldKeys, oldWritable
	})

	latestRelease = func(context.Context) (*release.GithubRelease, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.checks = append(f.checks, time.Now())
		if f.latestErr != nil {
			return nil, f.latestErr
		}
		return &release.GithubRelease{TagName: f.tag}, nil
	}
	signedChecksum = func(context.Context, *release.GithubRelease, map[string]ed25519.PublicKey, time.Time) (*release.Signed, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.signed++
		if f.signErr != nil {
			return nil, f.signErr
		}
		return &release.Signed{URL: "https://example.com/fly-linux-amd64.tar.gz", SHA256: "abc123", SignedAt: f.signedAt}, nil
	}
	releaseKeys = func() (map[string]ed25519.PublicKey, error) {
		return map[string]ed25519.PublicKey{"test": make(ed25519.PublicKey, ed25519.PublicKeySize)}, nil
	}
	binaryDirWritable = func() bool { return true }
}

func (f *fakeReleases) checkTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

// setReportInterval saves the report interval n in dir, as a reply would.
func setReportInterval(t *testing.T, dir string, n int) {
	t.Helper()
	if err := statefile.Write(filepath.Join(dir, "state.json"), state{ReportInterval: n}); err != nil {
		t.Fatal(err)
	}
}

// runAuto runs an agent with server id 17 (slot 00:17 UTC) and auto-update
// on, until it exits or until d passes. It returns true when it exited.
func runAuto(t *testing.T, d time.Duration, dir string, cp *fakeCP, c Collector) (exited bool, rec *recorder) {
	t.Helper()

	rec = &recorder{}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	cfg := Config{ServerID: 17, StateDir: dir, AutoUpdate: true}
	if err := run(ctx, cfg, slog.New(rec), cp, c); err != nil {
		t.Fatal(err)
	}
	return ctx.Err() == nil, rec
}

func TestAutoUpdateChecksOneTimeEachDay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		f := &fakeReleases{tag: "v0.1.1"}
		useFakeReleases(t, f)
		calls := fakeUpdate(t, nil)
		dir := t.TempDir()
		setReportInterval(t, dir, 10)

		exited, rec := runAuto(t, 72*time.Hour+time.Minute, dir, &fakeCP{}, nil)

		// The first check at the first tick, which is not a report tick. Then
		// one check each day at the slot of the server.
		want := []time.Time{
			at(0),
			bubbleStart.Add(24*time.Hour + 17*time.Minute + 17*time.Second),
			bubbleStart.Add(48*time.Hour + 17*time.Minute + 17*time.Second),
		}
		if got := f.checkTimes(); !equalTimes(got, want) {
			t.Errorf("checks at %v, want %v", got, want)
		}
		// The release is older: the agent does not fetch its signature.
		if exited || f.signed != 0 || len(*calls) != 0 {
			t.Errorf("exited %v, %d signature checks, %d installs; want none for an older release", exited, f.signed, len(*calls))
		}
		if len(rec.times("checked for a new release: none to install")) != 3 {
			t.Error("want one log record for each check")
		}
	})
}

func TestAutoUpdateInstallsAfterTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		// Signed one hour before the start: the first check is too early.
		f := &fakeReleases{tag: "v0.2.1", signedAt: bubbleStart.Add(-time.Hour)}
		useFakeReleases(t, f)
		calls := fakeUpdate(t, nil)
		dir := t.TempDir()
		setReportInterval(t, dir, 10)

		exited, rec := runAuto(t, 72*time.Hour, dir, &fakeCP{}, nil)

		if !exited {
			t.Fatal("the agent did not exit after the update")
		}
		if len(rec.times("a new release installs after its wait")) != 1 {
			t.Error("want one \"installs after its wait\" record, at the first check")
		}
		// The second check, on the second day, installs the release.
		checks := f.checkTimes()
		if len(checks) != 2 || !checks[1].Equal(bubbleStart.Add(24*time.Hour+17*time.Minute+17*time.Second)) {
			t.Errorf("checks at %v, want the install at the second check", checks)
		}
		want := wire.UpdateArgs{URL: "https://example.com/fly-linux-amd64.tar.gz", Version: "v0.2.1", SHA256: "abc123"}
		if len(*calls) != 1 || (*calls)[0] != want {
			t.Errorf("installs = %+v, want one install of %+v", *calls, want)
		}
	})
}

func TestAutoUpdateDoesNotInstall(t *testing.T) {
	old := bubbleStart.Add(-48 * time.Hour)
	tests := []struct {
		name     string
		running  string
		releases *fakeReleases
		wantLog  string
		wantSign int
	}{
		{"the same version", "v0.2.1", &fakeReleases{tag: "v0.2.1", signedAt: old}, "checked for a new release: none to install", 0},
		{"an older release", "v0.3.0", &fakeReleases{tag: "v0.2.1", signedAt: old}, "checked for a new release: none to install", 0},
		{"no signature", "v0.2.0", &fakeReleases{tag: "v0.2.1", signErr: release.ErrNotSigned}, "a new release waits for its signature", 1},
		{"a bad signature", "v0.2.0", &fakeReleases{tag: "v0.2.1", signErr: errors.New("the signature is not correct")}, "not installing a new release", 1},
		{"GitHub is down", "v0.2.0", &fakeReleases{latestErr: errors.New("unexpected response: 502")}, "checking for a new release failed", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				setVersion(t, tt.running)
				f := tt.releases
				useFakeReleases(t, f)
				calls := fakeUpdate(t, nil)
				dir := t.TempDir()
				setReportInterval(t, dir, 10)

				// Two hours: a failure waits for the next day, not for the
				// next tick.
				exited, rec := runAuto(t, 2*time.Hour, dir, &fakeCP{}, nil)

				if exited || len(*calls) != 0 {
					t.Errorf("exited %v with %d installs, want no install", exited, len(*calls))
				}
				if n := len(f.checkTimes()); n != 1 {
					t.Errorf("%d checks in two hours, want 1", n)
				}
				if f.signed != tt.wantSign {
					t.Errorf("%d signature checks, want %d", f.signed, tt.wantSign)
				}
				if len(rec.times(tt.wantLog)) != 1 {
					t.Errorf("no %q log record", tt.wantLog)
				}
			})
		})
	}
}

func TestAutoUpdateFailedInstallKeepsTheAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		useFakeReleases(t, &fakeReleases{tag: "v0.2.1", signedAt: bubbleStart.Add(-48 * time.Hour)})
		calls := fakeUpdate(t, errors.New("the sha256 of the download does not agree"))

		exited, rec := runAuto(t, 5*time.Minute, t.TempDir(), &fakeCP{}, nil)

		if exited || len(*calls) != 1 {
			t.Errorf("exited %v after %d installs, want one failed install and no exit", exited, len(*calls))
		}
		if len(rec.times("the update failed; the old binary continues")) != 1 {
			t.Error("no log record for the failed update")
		}
	})
}

func TestAutoUpdateIsOff(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, cfg *Config)
		wantLog string
	}{
		{"by the switch", func(_ *testing.T, cfg *Config) { cfg.AutoUpdate = false }, "auto-update is off"},
		{"for a dev build", func(t *testing.T, _ *Config) { setVersion(t, "dev") }, "auto-update is off: this build has no release version"},
		{"for a build after a tag", func(t *testing.T, _ *Config) { setVersion(t, "v0.2.0-3-gabcdef1") }, "auto-update is off: this build has no release version"},
		{"for a build with changes", func(t *testing.T, _ *Config) { setVersion(t, "v0.2.0-dirty") }, "auto-update is off: this build has no release version"},
		{"without a trusted key", func(*testing.T, *Config) {
			releaseKeys = func() (map[string]ed25519.PublicKey, error) { return nil, nil }
		}, "auto-update is off: this build trusts no release key"},
		{"with keys that are not valid", func(*testing.T, *Config) {
			releaseKeys = func() (map[string]ed25519.PublicKey, error) { return nil, errors.New("bad key") }
		}, "auto-update is off: the release keys of this build are not valid"},
		{"when the binary folder is not writable", func(*testing.T, *Config) {
			binaryDirWritable = func() bool { return false }
		}, "auto-update is off: the agent cannot write the folder of its binary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				setVersion(t, "v0.2.0")
				f := &fakeReleases{tag: "v0.2.1", signedAt: bubbleStart.Add(-48 * time.Hour)}
				useFakeReleases(t, f)
				cfg := Config{ServerID: 17, StateDir: t.TempDir(), AutoUpdate: true}
				tt.setup(t, &cfg)

				rec := &recorder{}
				ctx, cancel := context.WithTimeout(context.Background(), 48*time.Hour)
				defer cancel()
				if err := run(ctx, cfg, slog.New(rec), &fakeCP{}, nil); err != nil {
					t.Fatal(err)
				}

				if n := len(f.checkTimes()); n != 0 {
					t.Errorf("%d checks, want none: the agent must not call GitHub", n)
				}
				if len(rec.times(tt.wantLog)) != 1 {
					t.Errorf("no %q log record", tt.wantLog)
				}
			})
		})
	}
}

func TestAutoUpdateCheckTimeSurvivesARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		f := &fakeReleases{tag: "v0.1.1"}
		useFakeReleases(t, f)
		dir := t.TempDir()

		runAuto(t, 5*time.Minute, dir, &fakeCP{}, nil)
		runAuto(t, 5*time.Minute, dir, &fakeCP{}, nil)

		// The second process knows the check of the first one.
		if n := len(f.checkTimes()); n != 1 {
			t.Errorf("%d checks in two runs on one day, want 1", n)
		}
	})
}

func TestAutoUpdateCheckTimeInTheFuture(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		f := &fakeReleases{tag: "v0.1.1"}
		useFakeReleases(t, f)
		dir := t.TempDir()
		// A clock that was years ahead saved this time.
		saved := state{ReportInterval: 10, LastUpdateCheck: bubbleStart.AddDate(5, 0, 0)}
		if err := statefile.Write(filepath.Join(dir, "state.json"), saved); err != nil {
			t.Fatal(err)
		}

		runAuto(t, 2*time.Minute, dir, &fakeCP{}, nil)

		if got := f.checkTimes(); len(got) != 1 || !got[0].Equal(at(0)) {
			t.Errorf("checks at %v, want one check at the first tick", got)
		}
	})
}

func TestAutoUpdateWhileTheEventsFail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		f := &fakeReleases{tag: "v0.1.1"}
		useFakeReleases(t, f)
		cp := &fakeCP{eventsReply: func(int, *wire.EventsRequest) (*wire.EventsReply, error) {
			return nil, errors.New("connection refused")
		}}

		// Report interval 1: each tick is a report tick, and each report
		// stops before the poll.
		runAuto(t, 2*time.Minute, t.TempDir(), cp, nil)

		if got := f.checkTimes(); len(got) != 1 || !got[0].Equal(at(0)) {
			t.Errorf("checks at %v, want a check at the first tick", got)
		}
	})
}

func TestNewReportIntervalKeepsTheCheckTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		useFakeReleases(t, &fakeReleases{tag: "v0.1.1"})
		dir := t.TempDir()
		// The first reply sets 3, before the check. A later reply sets 5,
		// after the check: that write must keep the check time.
		cp := &fakeCP{metricsReply: func(call int, req *wire.MetricsRequest) (*wire.MetricsReply, error) {
			n := 5
			if call == 0 {
				n = 3
			}
			return &wire.MetricsReply{Accepted: len(req.Samples), ReportInterval: n}, nil
		}}

		runAuto(t, 5*time.Minute, dir, cp, &fakeCollector{})

		var s state
		if err := statefile.Read(filepath.Join(dir, "state.json"), &s); err != nil {
			t.Fatal(err)
		}
		if s.ReportInterval != 5 || !s.LastUpdateCheck.Equal(at(0)) {
			t.Errorf("state = %+v, want interval 5 and the check at %v", s, at(0))
		}
	})
}

func TestConfigWarningsAreLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		cfg := Config{ServerID: 17, StateDir: t.TempDir(), Warnings: []string{"FLY_AGENT_AUTO_UPDATE=\"of\" is not on or off; auto-update stays on"}}
		if err := run(ctx, cfg, slog.New(rec), &fakeCP{}, nil); err != nil {
			t.Fatal(err)
		}

		if len(rec.times(cfg.Warnings[0])) != 1 {
			t.Error("the warning of the configuration is not in the log")
		}
	})
}
