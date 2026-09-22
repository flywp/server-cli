package agent

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/flywp/server-cli/internal/statefile"
	"github.com/flywp/server-cli/internal/version"
)

// The report interval is the number of samples in one report. The control
// plane sets it in each reply.
const (
	minReportInterval = 1
	maxReportInterval = 10
)

// state is the part of the agent state that is not a queue.
type state struct {
	ReportInterval int `json:"report_interval"`
}

type agent struct {
	cfg Config
	log *slog.Logger

	// interval is the report interval, and pending is the number of samples
	// since the last report.
	interval int
	pending  int
}

// Run runs the agent until ctx is done. Only one agent can run with the same
// state directory.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	unlock, err := lock(cfg.StateDir)
	if err != nil {
		return err
	}
	defer unlock()

	a := &agent{cfg: cfg, log: log, interval: loadInterval(cfg.StateDir, log)}
	log.Info("agent started", "version", version.Version, "offset", cfg.Offset(), "report_interval", a.interval)
	a.loop(ctx)
	log.Info("agent stopped")

	return nil
}

// loop calls tick at the offset second of each minute until ctx is done.
func (a *agent) loop(ctx context.Context) {
	for {
		timer := time.NewTimer(time.Until(nextTick(time.Now(), a.cfg.Offset())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case now := <-timer.C:
			a.tick(ctx, now)
		}
	}
}

// tick does the work of one minute: it takes a sample and, after each
// interval samples, sends a report.
func (a *agent) tick(ctx context.Context, now time.Time) {
	a.log.Debug("tick", "at", now)

	a.pending++
	if a.pending >= a.interval {
		a.pending = 0
		a.report(ctx)
	}
}

// report sends the queued data to the control plane.
func (a *agent) report(_ context.Context) {
	a.log.Debug("report")
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
