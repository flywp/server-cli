package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/statefile"
	"github.com/flywp/server-cli/internal/version"
	"github.com/oklog/ulid/v2"
)

// bubbleStart is the time at which each synctest bubble starts.
var bubbleStart = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// at returns the time of the tick in minute m of the bubble, for offset 17s.
func at(m int) time.Time {
	return bubbleStart.Add(time.Duration(m)*time.Minute + 17*time.Second)
}

// runFor runs an agent with server id 17 for d in a synctest bubble, and
// returns the state directory and the log.
func runFor(t *testing.T, d time.Duration, dir string, cp *fakeCP, c Collector) *recorder {
	t.Helper()

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		done <- run(ctx, Config{ServerID: 17, StateDir: dir}, slog.New(rec), cp, c)
	}()

	time.Sleep(d)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	return rec
}

func equalTimes(got, want []time.Time) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !got[i].Equal(want[i]) {
			return false
		}
	}
	return true
}

func TestStartSendsAgentStartedAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cp := &fakeCP{}
		runFor(t, time.Second, t.TempDir(), cp, nil)

		if len(cp.events) != 1 || len(cp.events[0].Events) != 1 {
			t.Fatalf("events requests = %+v, want one request with agent.started", cp.events)
		}
		if !cp.eventsAt[0].Equal(bubbleStart) {
			t.Errorf("agent.started sent at %s, want at start, before the first tick", cp.eventsAt[0])
		}

		e := cp.events[0].Events[0]
		if e.Name != wire.EventAgentStarted || e.Data == nil || e.Data.Version != version.Version {
			t.Errorf("event = %+v, want agent.started with the version", e)
		}
		if _, err := ulid.ParseStrict(e.ID); err != nil {
			t.Errorf("event id %q is not a ULID: %v", e.ID, err)
		}
	})
}

func TestReportsFollowTheIntervalOfTheReply(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		cp := &fakeCP{metricsReply: func(_ int, req *wire.MetricsRequest) (*wire.MetricsReply, error) {
			return &wire.MetricsReply{Accepted: len(req.Samples), ReportInterval: 3}, nil
		}}
		collector := &fakeCollector{status: wire.Status{OS: "Ubuntu 24.04.1 LTS", Arch: "amd64"}}

		runFor(t, 7*time.Minute, dir, cp, collector)

		// Interval 1 until the first reply sets 3: reports at minute 0, 3 and 6.
		if want := []time.Time{at(0), at(3), at(6)}; !equalTimes(cp.metricsAt, want) {
			t.Errorf("reports at %v, want %v", cp.metricsAt, want)
		}
		if got := cp.sampleCounts(); fmt.Sprint(got) != "[1 3 3]" {
			t.Errorf("samples per report = %v, want [1 3 3]", got)
		}

		first := cp.metrics[0]
		if first.AgentVersion != version.Version {
			t.Errorf("agent_version = %q, want %q", first.AgentVersion, version.Version)
		}
		if first.Status == nil || first.Status.OS != "Ubuntu 24.04.1 LTS" || first.Status.Arch != "amd64" {
			t.Errorf("status = %+v, want the status of the collector in each report", first.Status)
		}
		if got := first.Samples[0].RecordedAt; !got.Equal(at(0)) {
			t.Errorf("recorded_at = %s, want the tick time %s", got, at(0))
		}

		var saved state
		if err := statefile.Read(filepath.Join(dir, "state.json"), &saved); err != nil || saved.ReportInterval != 3 {
			t.Errorf("saved state = %+v (%v), want report_interval 3", saved, err)
		}
	})
}

func TestMetricsReplyRules(t *testing.T) {
	tests := []struct {
		name string
		err  error
		// The times of the metrics requests, and the samples in each.
		wantAt     []time.Time
		wantCounts string
	}{
		{"400 drops the samples", &StatusError{StatusCode: http.StatusBadRequest}, []time.Time{at(0), at(1)}, "[1 1]"},
		{"422 drops the samples", &StatusError{StatusCode: http.StatusUnprocessableEntity}, []time.Time{at(0), at(1)}, "[1 1]"},
		{"401 keeps them for 5 minutes", &StatusError{StatusCode: http.StatusUnauthorized}, []time.Time{at(0), at(5)}, "[1 6]"},
		{"429 waits for Retry-After", &StatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 2 * time.Minute}, []time.Time{at(0), at(2)}, "[1 3]"},
		{"503 keeps them and waits 1 minute", &StatusError{StatusCode: http.StatusServiceUnavailable}, []time.Time{at(0), at(1)}, "[1 2]"},
		{"502 is the same as 503", &StatusError{StatusCode: http.StatusBadGateway}, []time.Time{at(0), at(1)}, "[1 2]"},
		{"a network error is the same as 503", errors.New("connection refused"), []time.Time{at(0), at(1)}, "[1 2]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// The first request fails, the next ones succeed.
				cp := &fakeCP{metricsReply: func(call int, req *wire.MetricsRequest) (*wire.MetricsReply, error) {
					if call == 0 {
						return nil, tt.err
					}
					return &wire.MetricsReply{Accepted: len(req.Samples)}, nil
				}}

				runFor(t, tt.wantAt[len(tt.wantAt)-1].Sub(bubbleStart)+time.Second, t.TempDir(), cp, &fakeCollector{})

				if !equalTimes(cp.metricsAt, tt.wantAt) {
					t.Errorf("requests at %v, want %v", cp.metricsAt, tt.wantAt)
				}
				if got := fmt.Sprint(cp.sampleCounts()); got != tt.wantCounts {
					t.Errorf("samples per request = %s, want %s", got, tt.wantCounts)
				}
			})
		})
	}
}

func TestBackoffDoublesUpToTenMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cp := &fakeCP{metricsReply: func(int, *wire.MetricsRequest) (*wire.MetricsReply, error) {
			return nil, &StatusError{StatusCode: http.StatusServiceUnavailable}
		}}

		runFor(t, 46*time.Minute, t.TempDir(), cp, &fakeCollector{})

		// Waits of 1, 2, 4, 8, 10 and 10 minutes.
		want := []time.Time{at(0), at(1), at(3), at(7), at(15), at(25), at(35), at(45)}
		if !equalTimes(cp.metricsAt, want) {
			t.Errorf("requests at %v, want %v", cp.metricsAt, want)
		}
	})
}

func TestEventsGoFirstAndKeepTheirID(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cp := &fakeCP{eventsReply: func(call int, req *wire.EventsRequest) (*wire.EventsReply, error) {
			if call == 0 {
				return nil, &StatusError{StatusCode: http.StatusServiceUnavailable}
			}
			return &wire.EventsReply{Accepted: len(req.Events)}, nil
		}}

		runFor(t, 2*time.Minute, t.TempDir(), cp, &fakeCollector{})

		if len(cp.events) != 2 {
			t.Fatalf("events requests = %d, want 2 (a failure at start, then a resend)", len(cp.events))
		}
		if a, b := cp.events[0].Events[0].ID, cp.events[1].Events[0].ID; a != b {
			t.Errorf("the resend has id %s, want the same id %s", b, a)
		}

		// The failure at start blocks the sends until minute 1: no metrics at
		// minute 0, and at minute 1 the events go before the samples.
		if want := []time.Time{bubbleStart, at(1)}; !equalTimes(cp.eventsAt, want) {
			t.Errorf("events at %v, want %v", cp.eventsAt, want)
		}
		if want := []time.Time{at(1)}; !equalTimes(cp.metricsAt, want) {
			t.Errorf("metrics at %v, want %v", cp.metricsAt, want)
		}
		if got := fmt.Sprint(cp.sampleCounts()); got != "[2]" {
			t.Errorf("samples per request = %s, want [2]", got)
		}
	})
}

func TestLargeQueuesGoInBatches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		samples := make([]wire.Sample, 1000)
		events := make([]wire.Event, 150)
		for i := range events {
			events[i] = wire.Event{ID: ulid.Make().String(), Name: wire.EventCommandCompleted}
		}
		if err := statefile.Write(filepath.Join(dir, "samples.json"), samples); err != nil {
			t.Fatal(err)
		}
		if err := statefile.Write(filepath.Join(dir, "events.json"), events); err != nil {
			t.Fatal(err)
		}

		cp := &fakeCP{}
		runFor(t, 30*time.Second, dir, cp, &fakeCollector{})

		// 150 events and agent.started: 100 + 51. 1000 samples and one new
		// sample: 4 × 240 + 41.
		var eventCounts []int
		for _, r := range cp.events {
			eventCounts = append(eventCounts, len(r.Events))
		}
		if got := fmt.Sprint(eventCounts); got != "[100 51]" {
			t.Errorf("events per request = %s, want [100 51]", got)
		}
		if got := fmt.Sprint(cp.sampleCounts()); got != "[240 240 240 240 41]" {
			t.Errorf("samples per request = %s, want [240 240 240 240 41]", got)
		}

		var left []wire.Sample
		if err := statefile.Read(filepath.Join(dir, "samples.json"), &left); err != nil || len(left) != 0 {
			t.Errorf("samples left on disk = %d (%v), want 0", len(left), err)
		}
	})
}

func TestCollectorErrorSkipsTheMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cp := &fakeCP{}
		rec := runFor(t, 2*time.Minute, t.TempDir(), cp, &fakeCollector{err: errors.New("no /proc")})

		if len(cp.metrics) != 0 {
			t.Errorf("metrics requests = %d, want none without samples", len(cp.metrics))
		}
		if len(rec.times("skipping the sample of this minute")) != 2 {
			t.Error("want one warning for each skipped minute")
		}
	})
}
