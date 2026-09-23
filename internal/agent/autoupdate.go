package agent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/release"
	"github.com/flywp/server-cli/internal/version"
	"golang.org/x/mod/semver"
	"golang.org/x/sys/unix"
)

const (
	// autoUpdateWait is the time between the signature of a release and its
	// install by the agents. In this time the maintainer can mark a bad
	// release as a pre-release: /releases/latest then stops returning it.
	autoUpdateWait = 24 * time.Hour

	// checkGap is the shortest time between two checks. With it, a clock
	// step cannot cause two checks in one day.
	checkGap = 12 * time.Hour
)

// The parts of an update by release. Tests replace them.
var (
	latestRelease  = release.LatestRelease
	signedChecksum = func(ctx context.Context, rel *release.GithubRelease, keys map[string]ed25519.PublicKey, now time.Time) (*release.Signed, error) {
		return release.SignedChecksum(ctx, rel, runtime.GOOS, runtime.GOARCH, keys, now)
	}
	releaseKeys       = release.TrustedKeys
	binaryDirWritable = func() bool {
		exe, err := os.Executable()
		if err != nil {
			return false
		}
		if exe, err = filepath.EvalSymlinks(exe); err != nil {
			return false
		}
		return unix.Access(filepath.Dir(exe), unix.W_OK) == nil
	}
)

// updateSlot is the time after 00:00 UTC at which the server checks for a new
// release each day. It spreads the installs of a release over one day, so
// that the first servers show a problem before the others get the release.
func updateSlot(serverID int64) time.Duration {
	return time.Duration(serverID%(24*60)) * time.Minute
}

// nextCheck returns the time of the next check for a new release: the first
// slot that comes more than checkGap after the last check. An agent that
// never checked checks at once.
func nextCheck(last time.Time, slot time.Duration) time.Time {
	if last.IsZero() {
		return time.Time{}
	}

	after := last.Add(checkGap)
	// Truncate counts from the zero time, which is a midnight UTC.
	t := after.UTC().Truncate(24 * time.Hour).Add(slot)
	if !t.After(after) {
		t = t.Add(24 * time.Hour)
	}

	return t
}

// startAutoUpdate decides at start whether the agent checks for new releases,
// and when it checks first.
func (a *agent) startAutoUpdate(now time.Time) {
	switch keys, err := releaseKeys(); {
	case !a.cfg.AutoUpdate:
		a.log.Info("auto-update is off", "key", EnvAutoUpdate)
		return
	case !semver.IsValid(version.Version):
		// A build without a release tag (go build gives "dev") has no
		// place in the order of the releases.
		a.log.Info("auto-update is off: this build has no release version", "version", version.Version)
		return
	case err != nil:
		a.log.Error("auto-update is off: the release keys of this build are not valid", "error", err)
		return
	case len(keys) == 0:
		a.log.Warn("auto-update is off: this build trusts no release key")
		return
	case !binaryDirWritable():
		a.log.Warn("auto-update is off: the agent cannot write the folder of its binary")
		return
	default:
		a.keys = keys
	}

	// A check time after now comes from a clock that was wrong. Without
	// this, the agent could wait for a day that is years away.
	if a.lastUpdateCheck.After(now) {
		a.log.Warn("the time of the last release check is in the future; checking again", "last", a.lastUpdateCheck)
		a.lastUpdateCheck = time.Time{}
	}

	a.autoUpdate = true
	a.nextUpdateCheck = nextCheck(a.lastUpdateCheck, updateSlot(a.cfg.ServerID))
}

// checkForUpdate installs the latest release when it is newer, has a valid
// signature and waited autoUpdateWait after the signature. It returns true
// when it installed the release: the process must exit, so that systemd
// starts the new binary.
func (a *agent) checkForUpdate(ctx context.Context, now time.Time) (exit bool) {
	// Save the time first: a crash or a failure waits for the next day, and
	// does not check at each restart.
	a.lastUpdateCheck = now
	a.nextUpdateCheck = nextCheck(now, updateSlot(a.cfg.ServerID))
	a.saveState()

	log := a.log.With("version", version.Version, "next_check", a.nextUpdateCheck)

	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	rel, err := latestRelease(ctx)
	if err != nil {
		log.Warn("checking for a new release failed", "error", err)
		return false
	}
	log = log.With("release", rel.TagName)

	// The agent never downgrades. A version without an order (a dev build)
	// does not update by itself.
	if cmp, ok := compareVersions(version.Version, rel.TagName); !ok || cmp >= 0 {
		log.Info("checked for a new release: none to install")
		return false
	}

	signed, err := signedChecksum(ctx, rel, a.keys, now)
	switch {
	case errors.Is(err, release.ErrNotSigned):
		log.Info("a new release waits for its signature")
		return false
	case err != nil:
		log.Error("not installing a new release", "error", err)
		return false
	}

	if ready := signed.SignedAt.Add(autoUpdateWait); now.Before(ready) {
		log.Info("a new release installs after its wait", "signed_at", signed.SignedAt, "ready_at", ready)
		return false
	}

	log.Info("installing a new release")
	if err := updateBinary(ctx, wire.UpdateArgs{URL: signed.URL, Version: rel.TagName, SHA256: signed.SHA256}); err != nil {
		log.Error("the update failed; the old binary continues", "error", err)
		return false
	}

	log.Info("installed the new release; exiting so that systemd starts it")
	return true
}
