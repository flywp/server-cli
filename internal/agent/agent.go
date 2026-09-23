package agent

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/release"
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
	PollCommands(ctx context.Context) (*wire.CommandsReply, error)
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
	// LastUpdateCheck is the time of the last check for a new release.
	LastUpdateCheck time.Time `json:"last_update_check,omitzero"`
}

type agent struct {
	cfg       Config
	log       *slog.Logger
	cp        ControlPlane
	collector Collector
	outbox    *outbox
	ledger    *ledger

	// interval is the report interval, and pending is the number of samples
	// since the last report.
	interval int
	pending  int

	// last is the time of the last tick.
	last time.Time

	// autoUpdate is true when the agent checks for a new release each day,
	// with keys. lastUpdateCheck is the time of the last check, and
	// nextUpdateCheck the time of the next one.
	autoUpdate      bool
	keys            map[string]ed25519.PublicKey
	lastUpdateCheck time.Time
	nextUpdateCheck time.Time

	// The waits of the events and the metrics requests after a failure, and
	// whether the last report sent all samples.
	eventsWait  backoff
	metricsWait backoff
	pollWait    backoff
	samplesSent bool
}

// Run runs the agent until ctx is done. Only one agent can run with the same
// state directory. A nil collector takes no samples.
func Run(ctx context.Context, cfg Config, log *slog.Logger, collector Collector) error {
	// A crash during an update can leave a download next to the binary.
	if exe, err := os.Executable(); err == nil {
		release.RemoveTemp(filepath.Dir(exe))
	}

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

	saved := loadState(cfg.StateDir, log)
	a := &agent{
		cfg:             cfg,
		log:             log,
		cp:              cp,
		collector:       collector,
		outbox:          loadOutbox(cfg.StateDir, log),
		ledger:          loadLedger(cfg.StateDir, log),
		interval:        saved.ReportInterval,
		lastUpdateCheck: saved.LastUpdateCheck,
	}
	log.Info("agent started", "version", version.Version, "offset", cfg.Offset(), "report_interval", a.interval)
	for _, w := range cfg.Warnings {
		log.Warn(w)
	}
	a.startAutoUpdate(time.Now())

	// Send the events at once, not at the next tick: after an update or a
	// restart, they hold the result of the command.
	a.addEvent(wire.EventAgentStarted, "", &wire.EventData{Version: version.Version})
	a.resolve()
	a.send(ctx, false)

	if a.loop(ctx) {
		// systemd starts the agent again (Restart=always), with the new
		// binary after an update.
		log.Info("agent exits; systemd starts it again")
		return nil
	}
	log.Info("agent stopped")

	return nil
}

// loop calls tick at the offset second of each minute until ctx is done. It
// returns true when a command ends the process.
func (a *agent) loop(ctx context.Context) (exit bool) {
	for {
		next := nextAfter(time.Now(), a.last, a.cfg.Offset())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
			a.last = next
			if a.tick(ctx, next) {
				return true
			}
		}
	}
}

// maxStepBack is the largest step back of the wall clock after which the loop
// still skips the minute that already ran. A larger step means that the clock
// was wrong before (for example a VM that booted with its clock ahead, then
// NTP): the loop follows the new clock at once, or it would wait, silent, for
// the size of the step. A minute that runs two times is harmless: the control
// plane keeps one sample for each minute.
const maxStepBack = 2 * time.Minute

// nextAfter returns the next tick after now. After a small step back of the
// wall clock, it never returns a tick at or before last. The timer runs on the
// monotonic clock, but the tick times come from the wall clock: when the wall
// clock steps back, the timer fires before the tick time, and without last
// the same tick would run two times.
func nextAfter(now, last time.Time, offset time.Duration) time.Time {
	next := nextTick(now, offset)
	if !last.IsZero() && !next.After(last) && last.Sub(now) < maxStepBack {
		next = nextTick(last, offset)
	}

	return next
}

// tick does the work of one minute: it takes a sample and, after each
// interval samples, sends a report and runs the new commands. One time each
// day it checks for a new release. It returns true when a command or an
// update ends the process.
func (a *agent) tick(ctx context.Context, now time.Time) (exit bool) {
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

	if a.report(ctx) {
		return true
	}

	// On each tick, not only after a report: a long report interval or a
	// control plane that is down must not move the check.
	if a.autoUpdate && !now.Before(a.nextUpdateCheck) {
		return a.checkForUpdate(ctx, now)
	}
	return false
}

// report sends a report after each interval samples, and runs the new
// commands. It returns true when a command ends the process.
func (a *agent) report(ctx context.Context) (exit bool) {
	a.pending++
	if a.pending < a.interval {
		return false
	}

	a.log.Debug("report")
	eventsSent := a.send(ctx, true)

	// Samples that could not go are tried again at the next tick, when their
	// wait allows it, not only after the next full interval.
	if a.samplesSent {
		a.pending = 0
	}

	// Poll only when no event waits: the results of the commands that ran
	// must reach the control plane first, so that it does not send them again.
	if !eventsSent {
		return false
	}
	if a.commands(ctx) {
		return true
	}

	// Send the results of the commands now, not at the next report.
	if len(a.outbox.events) > 0 {
		a.send(ctx, false)
	}
	return false
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
	a.saveState()
}

// saveState writes all of the state: a write of one field must not remove
// an other.
func (a *agent) saveState() {
	s := state{ReportInterval: a.interval, LastUpdateCheck: a.lastUpdateCheck}
	if err := statefile.Write(filepath.Join(a.cfg.StateDir, "state.json"), s); err != nil {
		a.log.Error("saving the state", "error", err)
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

// loadState returns the saved state. The report interval is the one of the
// last reply, or 1.
func loadState(dir string, log *slog.Logger) state {
	var s state
	err := statefile.Read(filepath.Join(dir, "state.json"), &s)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return state{ReportInterval: minReportInterval}
	case err != nil:
		log.Warn("ignoring the saved state", "error", err)
		return state{ReportInterval: minReportInterval}
	}

	s.ReportInterval = clampInterval(s.ReportInterval)
	return s
}

func clampInterval(n int) int {
	return min(max(n, minReportInterval), maxReportInterval)
}
