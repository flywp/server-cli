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

func TestNextAfterALargeClockStepBack(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	offset := 17 * time.Second

	tests := []struct {
		name string
		step time.Duration
		want time.Time
	}{
		// A step of 90 s is still small: skip to the minute after the last tick.
		{"90 seconds", 90 * time.Second, base.Add(time.Minute + offset)},
		// The clock was one hour ahead. Follow the new clock: the next tick
		// comes in less than one minute, not in one hour.
		{"one hour", time.Hour, base.Add(-time.Hour + time.Minute + offset)},
		{"one year", 365 * 24 * time.Hour, base.Add(-365*24*time.Hour + time.Minute + offset)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			last := base.Add(offset)
			now := last.Add(-tt.step).Add(time.Second)
			got := nextAfter(now, last, offset)
			if !got.Equal(tt.want) {
				t.Errorf("nextAfter() = %s, want %s", got, tt.want)
			}
			if wait := got.Sub(now); wait <= 0 || wait > tt.step+2*time.Minute {
				t.Errorf("the loop waits %v after a step back of %v", wait, tt.step)
			}
		})
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

func TestNextReading(t *testing.T) {
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		now    time.Time
		offset time.Duration
		want   time.Time
	}{
		{base, 17 * time.Second, base.Add(7 * time.Second)},
		{base.Add(7 * time.Second), 17 * time.Second, base.Add(17 * time.Second)},
		{base.Add(18 * time.Second), 17 * time.Second, base.Add(27 * time.Second)},
		{base.Add(58 * time.Second), 17 * time.Second, base.Add(67 * time.Second)},
		{base.Add(59*time.Second + 999*time.Millisecond), 0, base.Add(time.Minute)},
		{base.Add(3 * time.Second), 43 * time.Second, base.Add(3 * time.Second).Add(10 * time.Second)},
	}

	for _, tt := range tests {
		if got := nextReading(tt.now, tt.offset); !got.Equal(tt.want) {
			t.Errorf("nextReading(%s, %v) = %s, want %s", tt.now.Format(time.TimeOnly), tt.offset, got.Format(time.TimeOnly), tt.want.Format(time.TimeOnly))
		}
	}
}

func TestLoopReadsEach10SecondsBetweenTheTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		collector := &fakeCollector{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error)
		go func() {
			done <- run(ctx, Config{ServerID: 17, StateDir: t.TempDir()}, slog.New(slog.DiscardHandler), &fakeCP{}, collector)
		}()

		time.Sleep(2 * time.Minute)
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		// The ticks are at :17. The readings are at :07, :27, :37, :47 and :57:
		// the loop never reads at a tick, because the tick takes the reading.
		start := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		var want []time.Time
		for s := 7 * time.Second; s < 2*time.Minute; s += 10 * time.Second {
			if s%time.Minute != 17*time.Second {
				want = append(want, start.Add(s))
			}
		}
		got := collector.readTimes()
		if len(got) != len(want) {
			t.Fatalf("readings at %v, want %v", got, want)
		}
		for i := range want {
			if !got[i].Equal(want[i]) {
				t.Errorf("reading %d at %s, want %s", i, got[i].Format(time.TimeOnly), want[i].Format(time.TimeOnly))
			}
		}
		if collector.n != 2 {
			t.Errorf("%d samples, want 2 (at 0:17 and 1:17)", collector.n)
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

func TestLoadStateInterval(t *testing.T) {
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

			if got := loadState(dir, slog.New(&recorder{})).ReportInterval; got != tt.want {
				t.Errorf("loadState().ReportInterval = %d, want %d", got, tt.want)
			}
		})
	}
}
