package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/release"
	"github.com/flywp/server-cli/internal/statefile"
	"github.com/flywp/server-cli/internal/version"
	"golang.org/x/mod/semver"
)

const (
	// keepRan is how long the agent remembers a command that it ran. The
	// control plane sends a command for at most 24 hours, and it keeps the
	// event ids for 2 days.
	keepRan = 48 * time.Hour
	// updateTimeout limits the download and the install of an update.
	updateTimeout = 5 * time.Minute
)

// ranCommand is a command that the agent ran or started to run.
type ranCommand struct {
	ID   string `json:"id"`
	Verb string `json:"verb"`
	// Target is the version of an update.
	Target string    `json:"target_version,omitempty"`
	RanAt  time.Time `json:"ran_at"`
	// ResultSent is false until the result event is in the queue. An
	// update or a restart ends the process, so the next process sends it.
	ResultSent bool `json:"result_sent"`
}

// ledger is the list of the commands that the agent ran (ran.json). The poll
// sends each open command again until its result arrives, so the agent must
// remember a command to run it only one time.
type ledger struct {
	path    string
	log     *slog.Logger
	entries []ranCommand
}

func loadLedger(dir string, log *slog.Logger) *ledger {
	l := &ledger{path: filepath.Join(dir, "ran.json"), log: log}
	if err := statefile.Read(l.path, &l.entries); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Error("dropping the list of the commands that ran", "error", err)
		l.entries = nil
	}
	return l
}

func (l *ledger) has(id string) bool {
	return slices.ContainsFunc(l.entries, func(c ranCommand) bool { return c.ID == id })
}

// add records c and saves the list. The command stays in the list in memory
// also when the save fails.
func (l *ledger) add(c ranCommand) error {
	l.entries = append(l.entries, c)
	return statefile.Write(l.path, l.entries)
}

func (l *ledger) markSent(id string) {
	for i := range l.entries {
		if l.entries[i].ID == id {
			l.entries[i].ResultSent = true
		}
	}
	l.save()
}

// prune forgets the commands that finished more than keepRan ago.
func (l *ledger) prune(now time.Time) {
	n := len(l.entries)
	l.entries = slices.DeleteFunc(l.entries, func(c ranCommand) bool {
		return c.ResultSent && now.Sub(c.RanAt) > keepRan
	})
	if len(l.entries) != n {
		l.save()
	}
}

func (l *ledger) save() {
	if err := statefile.Write(l.path, l.entries); err != nil {
		l.log.Error("saving the list of the commands that ran", "error", err)
	}
}

// resolve puts the result of each command that the previous process started
// in the event queue: an update or a restart ends the process that runs it.
func (a *agent) resolve() {
	for _, c := range slices.Clone(a.ledger.entries) {
		if c.ResultSent {
			continue
		}

		if cmp, ok := compareVersions(version.Version, c.Target); c.Verb == wire.VerbUpdate && (!ok || cmp < 0) {
			a.addEvent(wire.EventCommandFailed, c.ID, &wire.EventData{
				Version: version.Version,
				Error:   fmt.Sprintf("the agent runs %s, not %s, after the update", version.Version, c.Target),
			})
		} else {
			a.addEvent(wire.EventCommandCompleted, c.ID, &wire.EventData{Version: version.Version})
		}
		a.ledger.markSent(c.ID)
	}
}

// commands polls for the open commands and runs the new ones, oldest first.
// It returns true when the process must exit: after an update or a restart.
// The next process does the other commands.
func (a *agent) commands(ctx context.Context) (exit bool) {
	start := time.Now()
	if a.pollWait.waiting(start) {
		return false
	}

	reply, err := a.cp.PollCommands(ctx)
	if a.outcome(ctx, &a.pollWait, start, err, "the poll", 0) != sent {
		return false
	}

	a.ledger.prune(time.Now())
	for _, c := range reply.Commands {
		if a.ledger.has(c.ID) {
			continue
		}
		if a.runCommand(ctx, c) {
			return true
		}
	}

	return false
}

// runCommand runs one command. It returns true when the process must exit.
func (a *agent) runCommand(ctx context.Context, c wire.Command) (exit bool) {
	log := a.log.With("command", c.ID, "verb", c.Verb)

	switch c.Verb {
	case wire.VerbRestart:
		// Record the command before the exit. Without the record, the next
		// process gets the same command and restarts again.
		if err := a.ledger.add(ranCommand{ID: c.ID, Verb: c.Verb, RanAt: time.Now()}); err != nil {
			a.fail(c, fmt.Errorf("saving the list of the commands that ran: %w", err))
			return false
		}
		log.Info("exiting for a restart; systemd starts the agent again")
		return true

	case wire.VerbUpdate:
		return a.update(ctx, c, log)

	default:
		log.Warn("not running a command with an unknown verb")
		a.addEvent(wire.EventCommandUnknown, c.ID, nil)
		a.record(c, "")
		return false
	}
}

// update installs the release of an agent.update command.
func (a *agent) update(ctx context.Context, c wire.Command, log *slog.Logger) (exit bool) {
	var args wire.UpdateArgs
	if err := json.Unmarshal(c.Args, &args); err != nil || args.URL == "" || args.Version == "" || args.SHA256 == "" {
		a.fail(c, errors.New("the arguments of agent.update are not valid: url, version and sha256 are necessary"))
		return false
	}

	// The agent never downgrades: a bad release is fixed with a newer one.
	// The same version completes the command; an older target fails it, so
	// the control plane sees that its version was not installed.
	switch cmp, ok := compareVersions(version.Version, args.Version); {
	case ok && cmp == 0:
		log.Info("the agent already runs this version", "version", version.Version)
		a.addEvent(wire.EventCommandCompleted, c.ID, &wire.EventData{Version: version.Version})
		a.record(c, args.Version)
		return false
	case ok && cmp > 0:
		a.fail(c, fmt.Errorf("the agent runs %s, newer than %s; it does not downgrade", version.Version, args.Version))
		return false
	}

	// Record the command before the update: after the exit, the next process
	// sends the result.
	if err := a.ledger.add(ranCommand{ID: c.ID, Verb: c.Verb, Target: args.Version, RanAt: time.Now()}); err != nil {
		a.fail(c, fmt.Errorf("saving the list of the commands that ran: %w", err))
		return false
	}

	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	if err := updateBinary(ctx, args); err != nil {
		log.Error("the update failed; the old binary continues", "error", err)
		a.addEvent(wire.EventCommandFailed, c.ID, &wire.EventData{Version: version.Version, Error: err.Error()})
		a.ledger.markSent(c.ID)
		return false
	}

	log.Info("installed the new binary; exiting so that systemd starts it", "version", args.Version)
	return true
}

// fail puts command.failed for c in the queue, and records c.
func (a *agent) fail(c wire.Command, err error) {
	a.log.Error("the command failed", "command", c.ID, "verb", c.Verb, "error", err)
	a.addEvent(wire.EventCommandFailed, c.ID, &wire.EventData{Version: version.Version, Error: err.Error()})
	a.record(c, "")
}

// record adds c, with its result already in the queue, to the ledger.
func (a *agent) record(c wire.Command, target string) {
	if a.ledger.has(c.ID) {
		a.ledger.markSent(c.ID)
		return
	}
	if err := a.ledger.add(ranCommand{ID: c.ID, Verb: c.Verb, Target: target, RanAt: time.Now(), ResultSent: true}); err != nil {
		a.log.Error("saving the list of the commands that ran", "error", err)
	}
}

// updateBinary downloads the release of args, checks its sha256 and puts it
// in place of the running binary. Tests replace it.
var updateBinary = func(ctx context.Context, args wire.UpdateArgs) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}

	// The download goes next to the binary, so that the rename in Install
	// does not cross file systems.
	archive, err := release.Download(ctx, args.URL, args.SHA256, filepath.Dir(exe))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(archive) }()

	return release.Install(archive, exe, release.BinaryName(runtime.GOOS, runtime.GOARCH))
}

// compareVersions compares the versions a and b (release tags, for example
// v0.2.1) with semver: -1, 0 or +1. ok is false when they have no order:
// a version that is not semver (for example "dev"), or two dev tags of one
// release (v0.2.0-dev.1a2b3c4 and v0.2.0-dev.9f8e7d6), whose commit hashes
// have no order. Equal strings always compare as 0.
func compareVersions(a, b string) (cmp int, ok bool) {
	if a == b {
		return 0, true
	}
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return 0, false
	}

	pa, pb := semver.Prerelease(a), semver.Prerelease(b)
	if pa != "" && pb != "" && strings.TrimSuffix(semver.Canonical(a), pa) == strings.TrimSuffix(semver.Canonical(b), pb) {
		return 0, false
	}

	return semver.Compare(a, b), true
}

// PollCommands gets the open commands (contract section 5).
func (c *Client) PollCommands(ctx context.Context) (*wire.CommandsReply, error) {
	var reply wire.CommandsReply
	if err := c.do(ctx, http.MethodGet, "agent/v1/commands", nil, &reply); err != nil {
		return nil, err
	}

	return &reply, nil
}
