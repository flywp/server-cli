package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/flywp/server-cli/internal/statefile"
)

// recorder is a slog handler that keeps the message and the time of each
// record, so that tests can see when the agent did its work.
type recorder struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *recorder) WithGroup(string) slog.Handler            { return r }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	return nil
}

// recordsOf returns the records with message msg.
func (r *recorder) recordsOf(msg string) []slog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []slog.Record
	for _, rec := range r.records {
		if rec.Message == msg {
			out = append(out, rec)
		}
	}
	return out
}

// attr returns the value of the attribute key of a record, as a string, or
// nil.
func attr(rec slog.Record, key string) any {
	var v any
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.String()
			return false
		}
		return true
	})
	return v
}

// times returns the times of the records with message msg.
func (r *recorder) times(msg string) []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	var ts []time.Time
	for _, rec := range r.records {
		if rec.Message == msg {
			ts = append(ts, rec.Time)
		}
	}
	return ts
}

func TestNextTick(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		now    time.Time
		offset time.Duration
		want   time.Time
	}{
		{base, 17 * time.Second, base.Add(17 * time.Second)},
		{base.Add(10 * time.Second), 17 * time.Second, base.Add(17 * time.Second)},
		// At the offset itself, the next tick is one minute later.
		{base.Add(17 * time.Second), 17 * time.Second, base.Add(77 * time.Second)},
		{base.Add(30 * time.Second), 17 * time.Second, base.Add(77 * time.Second)},
		{base.Add(59*time.Second + 999*time.Millisecond), 0, base.Add(time.Minute)},
	}

	for _, tt := range tests {
		if got := nextTick(tt.now, tt.offset); !got.Equal(tt.want) {
			t.Errorf("nextTick(%s, %v) = %s, want %s", tt.now.Format(time.TimeOnly), tt.offset, got.Format(time.TimeOnly), tt.want.Format(time.TimeOnly))
		}
	}
}

func TestNextAfterAClockStepBack(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	last := base.Add(17 * time.Second)

	// The wall clock stepped back 2 s after the tick at :17, so the timer
	// fired at :15 wall time. The next tick must be in the next minute.
	if got, want := nextAfter(base.Add(15*time.Second), last, 17*time.Second), base.Add(77*time.Second); !got.Equal(want) {
		t.Errorf("nextAfter() = %s, want %s: never the same tick two times", got.Format(time.TimeOnly), want.Format(time.TimeOnly))
	}
	// Without a step, the last tick does not change the result.
	if got, want := nextAfter(base.Add(30*time.Second), last, 17*time.Second), base.Add(77*time.Second); !got.Equal(want) {
		t.Errorf("nextAfter() = %s, want %s", got.Format(time.TimeOnly), want.Format(time.TimeOnly))
	}
	if got, want := nextAfter(base, time.Time{}, 17*time.Second), last; !got.Equal(want) {
		t.Errorf("nextAfter() without a last tick = %s, want %s", got.Format(time.TimeOnly), want.Format(time.TimeOnly))
	}
}

func TestLoopTicksAtTheOffsetAndReportsEachInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := statefile.Write(filepath.Join(dir, "state.json"), state{ReportInterval: 2}); err != nil {
			t.Fatal(err)
		}

		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error)
		go func() {
			done <- run(ctx, Config{ServerID: 17, StateDir: dir}, slog.New(rec), &fakeCP{}, nil)
		}()

		// The bubble starts at 2000-01-01 00:00:00 UTC. Let 4 minutes pass.
		time.Sleep(4 * time.Minute)
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		ticks := rec.times("tick")
		if len(ticks) != 4 {
			t.Fatalf("ticks at %v, want 4 ticks", ticks)
		}
		for i, got := range ticks {
			if want := start.Add(time.Duration(i)*time.Minute + 17*time.Second); !got.Equal(want) {
				t.Errorf("tick %d at %s, want %s", i, got.Format(time.TimeOnly), want.Format(time.TimeOnly))
			}
		}

		// Report interval 2: a report after tick 2 and tick 4.
		if reports := rec.times("report"); len(reports) != 2 || !reports[0].Equal(ticks[1]) || !reports[1].Equal(ticks[3]) {
			t.Errorf("reports at %v, want at ticks 2 and 4 (%v)", reports, ticks)
		}
		if len(rec.times("agent stopped")) != 1 {
			t.Error("no \"agent stopped\" log record")
		}
	})
}

func TestRunRefusesASecondAgent(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	if err := run(context.Background(), Config{StateDir: dir}, slog.New(&recorder{}), &fakeCP{}, nil); err == nil {
		t.Fatal("Run() = nil, want an error while a different agent holds the lock")
	}
}

func TestLoadInterval(t *testing.T) {
	tests := []struct {
		name  string
		saved *state
		want  int
	}{
		{"no state", nil, 1},
		{"saved", &state{ReportInterval: 5}, 5},
		{"too low", &state{ReportInterval: 0}, 1},
		{"too high", &state{ReportInterval: 60}, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.saved != nil {
				if err := statefile.Write(filepath.Join(dir, "state.json"), tt.saved); err != nil {
					t.Fatal(err)
				}
			}

			if got := loadInterval(dir, slog.New(&recorder{})); got != tt.want {
				t.Errorf("loadInterval() = %d, want %d", got, tt.want)
			}
		})
	}
}
