package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/version"
)

// setVersion sets the version of the running agent for one test.
func setVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

// fakeUpdate replaces the download and the install of an update.
func fakeUpdate(t *testing.T, err error) *[]wire.UpdateArgs {
	t.Helper()
	var calls []wire.UpdateArgs
	old := updateBinary
	updateBinary = func(_ context.Context, args wire.UpdateArgs) error {
		calls = append(calls, args)
		return err
	}
	t.Cleanup(func() { updateBinary = old })
	return &calls
}

func updateCommand(t *testing.T, id, v string) wire.Command {
	t.Helper()
	args, err := json.Marshal(wire.UpdateArgs{URL: "https://example.com/fly-linux-amd64.tar.gz", Version: v, SHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"})
	if err != nil {
		t.Fatal(err)
	}
	return wire.Command{ID: id, Verb: wire.VerbUpdate, Args: args}
}

// start runs an agent with server id 17 in the bubble until it exits or until
// d passes. It returns true when the agent exited for a command.
func start(t *testing.T, d time.Duration, dir string, cp *fakeCP) (exited bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if err := run(ctx, Config{ServerID: 17, StateDir: dir}, slog.New(&recorder{}), cp, &fakeCollector{}); err != nil {
		t.Fatal(err)
	}
	return ctx.Err() == nil
}

func eventNames(events []wire.Event) []string {
	var names []string
	for _, e := range events {
		names = append(names, e.Name)
	}
	return names
}

func TestRestartRunsOneTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		// The worst case: the command stays open after its result.
		cp := &fakeCP{commands: []wire.Command{{ID: "01JBX0000000000000000000R1", Verb: wire.VerbRestart, Args: json.RawMessage(`{}`)}}, keepOpen: true}

		if !start(t, 5*time.Minute, dir, cp) {
			t.Fatal("the agent did not exit for agent.restart")
		}
		if !time.Now().Equal(at(0)) {
			t.Errorf("the agent exited at %s, want at the first report %s", time.Now(), at(0))
		}
		if got := cp.results("01JBX0000000000000000000R1"); len(got) != 0 {
			t.Errorf("results before the exit = %v, want none: the new process sends the result", eventNames(got))
		}

		// systemd starts the agent again. The command is still open, but the
		// agent does not restart again.
		if start(t, 5*time.Minute, dir, cp) {
			t.Fatal("the new process exited again for the same agent.restart")
		}
		got := cp.results("01JBX0000000000000000000R1")
		if len(got) != 1 || got[0].Name != wire.EventCommandCompleted {
			t.Errorf("results = %v, want one command.completed", eventNames(got))
		}
	})
}

func TestUnknownVerbIsNotRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cp := &fakeCP{commands: []wire.Command{{ID: "01JBX0000000000000000000V1", Verb: "agent.shell", Args: json.RawMessage(`{"cmd":"rm -rf /"}`)}}, keepOpen: true}
		calls := fakeUpdate(t, nil)

		if start(t, 3*time.Minute, t.TempDir(), cp) {
			t.Fatal("the agent exited for an unknown verb")
		}
		got := cp.results("01JBX0000000000000000000V1")
		if len(got) != 1 || got[0].Name != wire.EventCommandUnknown {
			t.Errorf("results = %v, want one command.unknown in 3 polls", eventNames(got))
		}
		if len(*calls) != 0 {
			t.Errorf("updates = %d, want none", len(*calls))
		}
	})
}

func TestUpdateToTheRunningVersionDoesNotDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.3.0")
		calls := fakeUpdate(t, nil)
		cp := &fakeCP{commands: []wire.Command{updateCommand(t, "01JBX0000000000000000000A1", "v0.3.0")}}

		if start(t, 2*time.Minute, t.TempDir(), cp) {
			t.Fatal("the agent exited for an update to its own version")
		}
		if len(*calls) != 0 {
			t.Errorf("downloads = %d, want none", len(*calls))
		}
		got := cp.results("01JBX0000000000000000000A1")
		if len(got) != 1 || got[0].Name != wire.EventCommandCompleted || got[0].Data.Version != "v0.3.0" {
			t.Errorf("results = %+v, want command.completed with v0.3.0", got)
		}
	})
}

func TestFailedUpdateKeepsTheOldBinary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		setVersion(t, "v0.2.0")
		calls := fakeUpdate(t, errors.New("the sha256 of the archive is abc, not 9f86"))
		cp := &fakeCP{commands: []wire.Command{updateCommand(t, "01JBX0000000000000000000F1", "v0.3.0")}, keepOpen: true}

		if start(t, 3*time.Minute, t.TempDir(), cp) {
			t.Fatal("the agent exited after a failed update")
		}
		if len(*calls) != 1 {
			t.Errorf("downloads = %d, want 1: the agent does not try the same command again", len(*calls))
		}
		got := cp.results("01JBX0000000000000000000F1")
		if len(got) != 1 || got[0].Name != wire.EventCommandFailed || got[0].Data.Error == "" {
			t.Errorf("results = %+v, want one command.failed with the error", got)
		}
	})
}

func TestUpdateExitsAndTheNewProcessReports(t *testing.T) {
	tests := []struct {
		name, newVersion, want string
	}{
		{"new version runs", "v0.3.0", wire.EventCommandCompleted},
		{"a newer release runs", "v0.3.1", wire.EventCommandCompleted},
		// For example, the process stopped during the download.
		{"old version still runs", "v0.2.0", wire.EventCommandFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				setVersion(t, "v0.2.0")
				calls := fakeUpdate(t, nil)
				cp := &fakeCP{commands: []wire.Command{updateCommand(t, "01JBX0000000000000000000N1", "v0.3.0")}}

				if !start(t, 2*time.Minute, dir, cp) {
					t.Fatal("the agent did not exit after the update")
				}
				if len(*calls) != 1 || (*calls)[0].Version != "v0.3.0" {
					t.Fatalf("updates = %+v, want one to v0.3.0", *calls)
				}

				// systemd starts the binary that is now on the disk.
				version.Version = tt.newVersion
				if start(t, 2*time.Minute, dir, cp) {
					t.Fatal("the new process exited again")
				}
				got := cp.results("01JBX0000000000000000000N1")
				if len(got) != 1 || got[0].Name != tt.want || got[0].Data.Version != tt.newVersion {
					t.Errorf("results = %+v, want one %s with version %s", got, tt.want, tt.newVersion)
				}
				if len(*calls) != 1 {
					t.Errorf("updates = %d, want 1", len(*calls))
				}
			})
		})
	}
}

func TestUpdateWithArgumentsThatAreNotValid(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := fakeUpdate(t, nil)
		cp := &fakeCP{commands: []wire.Command{{ID: "01JBX0000000000000000000B1", Verb: wire.VerbUpdate, Args: json.RawMessage(`{"url":"https://example.com"}`)}}}

		if start(t, time.Minute, t.TempDir(), cp) {
			t.Fatal("the agent exited for an update that is not valid")
		}
		got := cp.results("01JBX0000000000000000000B1")
		if len(got) != 1 || got[0].Name != wire.EventCommandFailed || len(*calls) != 0 {
			t.Errorf("results = %v and %d downloads, want one command.failed and no download", eventNames(got), len(*calls))
		}
	})
}

func TestCommandsAfterAnExitWaitForTheNextProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		cp := &fakeCP{commands: []wire.Command{
			{ID: "01JBX0000000000000000000C1", Verb: wire.VerbRestart, Args: json.RawMessage(`{}`)},
			{ID: "01JBX0000000000000000000C2", Verb: "agent.unknown", Args: json.RawMessage(`{}`)},
		}}

		if !start(t, time.Minute, dir, cp) {
			t.Fatal("the agent did not exit for agent.restart")
		}
		if got := cp.results("01JBX0000000000000000000C2"); len(got) != 0 {
			t.Errorf("the second command ran before the exit: %v", eventNames(got))
		}

		start(t, 2*time.Minute, dir, cp)
		if got := cp.results("01JBX0000000000000000000C2"); len(got) != 1 || got[0].Name != wire.EventCommandUnknown {
			t.Errorf("results of the second command = %v, want command.unknown from the new process", eventNames(got))
		}
	})
}

func TestNoPollWhileTheControlPlaneIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cp := &fakeCP{eventsReply: func(int, *wire.EventsRequest) (*wire.EventsReply, error) {
			return nil, &StatusError{StatusCode: 503}
		}}

		start(t, 30*time.Second, t.TempDir(), cp)
		if len(cp.pollAt) != 0 {
			t.Errorf("polls = %d, want none while the events cannot be sent", len(cp.pollAt))
		}
	})
}

func TestVersionMatches(t *testing.T) {
	tests := []struct {
		target, running string
		want            bool
	}{
		{"v0.3.0", "v0.3.0", true},
		{"v0.3.0", "v0.3.1", true},
		{"v0.3.0", "v1.0.0", true},
		{"v0.3.0", "v0.2.9", false},
		{"v0.3.0", "0.3.0", false},
		// Dev tags have no order: only the same tag matches.
		{"v0.2.0-dev.1a2b3c4", "v0.2.0-dev.1a2b3c4", true},
		{"v0.2.0-dev.1a2b3c4", "v0.2.0-dev.9f8e7d6", false},
		{"v0.2.0-dev.9f8e7d6", "v0.2.0-dev.1a2b3c4", false},
		{"v0.2.0-dev.1a2b3c4", "v0.2.0", false},
		{"v0.2.0", "v0.2.0-dev.1a2b3c4", false},
		{"v0.2.0", "dev", false},
	}

	for _, tt := range tests {
		if got := versionMatches(tt.target, tt.running); got != tt.want {
			t.Errorf("versionMatches(%q, %q) = %v, want %v", tt.target, tt.running, got, tt.want)
		}
	}
}

func TestLedgerForgetsOldCommands(t *testing.T) {
	l := loadLedger(t.TempDir(), slog.New(&recorder{}))
	now := time.Now()
	for _, c := range []ranCommand{
		{ID: "old-done", RanAt: now.Add(-49 * time.Hour), ResultSent: true},
		{ID: "old-open", RanAt: now.Add(-49 * time.Hour)},
		{ID: "new-done", RanAt: now.Add(-time.Hour), ResultSent: true},
	} {
		if err := l.add(c); err != nil {
			t.Fatal(err)
		}
	}

	l.prune(now)
	again := loadLedger(l.path[:len(l.path)-len("/ran.json")], slog.New(&recorder{}))
	for id, want := range map[string]bool{"old-done": false, "old-open": true, "new-done": true} {
		if again.has(id) != want {
			t.Errorf("has(%s) = %v after prune, want %v", id, !want, want)
		}
	}
}
