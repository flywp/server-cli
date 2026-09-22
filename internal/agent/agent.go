package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/statefile"
	"github.com/flywp/server-cli/internal/version"
	"github.com/oklog/ulid/v2"
)

// The report interval is the number of samples in one report. The control
// plane sets it in each reply.
const (
	minReportInterval = 1
	maxReportInterval = 10
)

// ControlPlane is the part of the control plane that the agent uses.
type ControlPlane interface {
	PostMetrics(ctx context.Context, req *wire.MetricsRequest) (*wire.MetricsReply, error)
	PostEvents(ctx context.Context, req *wire.EventsRequest) (*wire.EventsReply, error)
}

// Collector measures the server.
type Collector interface {
	// Sample measures the minute that ends at now.
	Sample(now time.Time) (wire.Sample, error)
	// Status describes the server now.
	Status(ctx context.Context) wire.Status
}

// state is the part of the agent state that is not a queue.
type state struct {
	ReportInterval int `json:"report_interval"`
}

type agent struct {
	cfg       Config
	log       *slog.Logger
	cp        ControlPlane
	collector Collector
	outbox    *outbox

	// interval is the report interval, and pending is the number of samples
	// since the last report.
	interval int
	pending  int

	// last is the time of the last tick.
	last time.Time

	// The waits of the events and the metrics requests after a failure, and
	// whether the last report sent all samples.
	eventsWait  backoff
	metricsWait backoff
	samplesSent bool
}

// Run runs the agent until ctx is done. Only one agent can run with the same
// state directory. A nil collector takes no samples.
func Run(ctx context.Context, cfg Config, log *slog.Logger, collector Collector) error {
	return run(ctx, cfg, log, NewClient(cfg, nil), collector)
}

func run(ctx context.Context, cfg Config, log *slog.Logger, cp ControlPlane, collector Collector) error {
	unlock, err := lock(cfg.StateDir)
	if err != nil {
		return err
	}
	defer unlock()

	// A crash during a write can leave a temporary file.
	statefile.RemoveTemp(cfg.StateDir)

	a := &agent{
		cfg:       cfg,
		log:       log,
		cp:        cp,
		collector: collector,
		outbox:    loadOutbox(cfg.StateDir, log),
		interval:  loadInterval(cfg.StateDir, log),
	}
	log.Info("agent started", "version", version.Version, "offset", cfg.Offset(), "report_interval", a.interval)

	// Send the events at once, not at the next tick: after an update or a
	// restart, they hold the result of the command.
	a.addEvent(wire.EventAgentStarted, "", &wire.EventData{Version: version.Version})
	a.send(ctx, false)

	a.loop(ctx)
	log.Info("agent stopped")

	return nil
}

// loop calls tick at the offset second of each minute until ctx is done.
func (a *agent) loop(ctx context.Context) {
	for {
		next := nextAfter(time.Now(), a.last, a.cfg.Offset())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			a.last = next
			a.tick(ctx, next)
		}
	}
}

// nextAfter returns the next tick after now, and never a tick at or before
// last. The timer runs on the monotonic clock, but the tick times come from
// the wall clock: when the wall clock steps back, the timer fires before the
// tick time, and without last the same tick would run two times.
func nextAfter(now, last time.Time, offset time.Duration) time.Time {
	next := nextTick(now, offset)
	if !last.IsZero() && !next.After(last) {
		next = nextTick(last, offset)
	}

	return next
}

// tick does the work of one minute: it takes a sample and, after each
// interval samples, sends a report.
func (a *agent) tick(ctx context.Context, now time.Time) {
	a.log.Debug("tick", "at", now)

	if a.collector != nil {
		s, err := a.collector.Sample(now)
		if err != nil {
			a.log.Warn("skipping the sample of this minute", "error", err)
		} else {
			s.RecordedAt = now
			a.outbox.addSample(cleanSample(s))
		}
	}

	a.pending++
	if a.pending < a.interval {
		return
	}

	a.log.Debug("report")
	a.send(ctx, true)

	// Samples that could not go are tried again at the next tick, when their
	// wait allows it, not only after the next full interval.
	if a.samplesSent {
		a.pending = 0
	}
}

// addEvent puts an event in the queue. Its ID is made now, so that each resend
// has the same ID.
func (a *agent) addEvent(name, commandID string, data *wire.EventData) {
	a.outbox.addEvent(cleanEvent(wire.Event{
		ID:        ulid.Make().String(),
		Name:      name,
		CommandID: commandID,
		At:        time.Now().UTC(),
		Data:      data,
	}))
}

// setInterval applies the report interval of a reply.
func (a *agent) setInterval(n int) {
	if n < minReportInterval || n > maxReportInterval || n == a.interval {
		return
	}

	a.log.Info("report interval changed", "from", a.interval, "to", n)
	a.interval = n
	if err := statefile.Write(filepath.Join(a.cfg.StateDir, "state.json"), state{ReportInterval: n}); err != nil {
		a.log.Error("saving the report interval", "error", err)
	}
}

// nextTick returns the first time after now that is offset past a full minute.
// It comes from the wall clock, so a slow tick moves to the next minute and
// never runs two times in one minute.
func nextTick(now time.Time, offset time.Duration) time.Time {
	t := now.Truncate(time.Minute).Add(offset)
	if !t.After(now) {
		t = t.Add(time.Minute)
	}

	return t
}

// loadInterval returns the report interval of the last reply, or 1.
func loadInterval(dir string, log *slog.Logger) int {
	var s state
	err := statefile.Read(filepath.Join(dir, "state.json"), &s)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return minReportInterval
	case err != nil:
		log.Warn("ignoring the saved state", "error", err)
		return minReportInterval
	}

	return clampInterval(s.ReportInterval)
}

func clampInterval(n int) int {
	return min(max(n, minReportInterval), maxReportInterval)
}
