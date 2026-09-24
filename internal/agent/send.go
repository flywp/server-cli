package agent

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/version"
)

const (
	// sendBudget limits the sends of one report, so that the next tick
	// comes on time.
	sendBudget = 45 * time.Second

	// The contract limits each request.
	maxSamplesPerRequest = 240
	maxEventsPerRequest  = 100

	// unauthorizedWait is the wait after a 401: the token is unknown or
	// revoked, and the agent keeps trying (contract section 4).
	unauthorizedWait = 5 * time.Minute
	// throttledWait is the wait after a 429 without a Retry-After header.
	throttledWait = time.Minute
	// The wait after other failures doubles from 1 minute up to 10 minutes.
	firstBackoff = time.Minute
	maxBackoff   = 10 * time.Minute
)

// outcome tells what to do with the data of a request.
type outcome int

const (
	// sent: drop the data, and send the next request.
	sent outcome = iota
	// refused: the control plane will never accept the data. Drop it, and
	// send the next request.
	refused
	// later: keep the data, and send no more of it now.
	later
)

// backoff is the wait of one kind of request after a failure. Each kind
// waits on its own: a broken events route must not stop the metrics.
type backoff struct {
	// retryAt is the earliest time of the next request, and failures is the
	// number of failed requests in a row. failingSince is the start of the
	// first report whose request failed and kept its data, or zero.
	retryAt      time.Time
	failures     int
	failingSince time.Time
}

// failed records a failure that keeps the data.
func (b *backoff) failed(start time.Time) {
	if b.failingSince.IsZero() {
		b.failingSince = start
	}
}

// waiting reports whether the request must wait at the time of the report.
func (b *backoff) waiting(at time.Time) bool {
	return at.Before(b.retryAt)
}

// send sends the events and then, with report, the samples. It returns true
// when no event is left in the queue, so that the commands can come next.
func (a *agent) send(ctx context.Context, report bool) (eventsSent bool) {
	// The waits count from the start of the report, not from the end of a
	// request: a 5 minute wait then ends at the tick 5 minutes later.
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, sendBudget)
	defer cancel()

	eventsSent = a.sendEvents(ctx, start)
	if report {
		a.samplesSent = a.sendSamples(ctx, start)
	}

	return eventsSent
}

// sendEvents sends the queued events, oldest first. It returns true when the
// event queue is empty.
func (a *agent) sendEvents(ctx context.Context, start time.Time) bool {
	if a.eventsWait.waiting(start) {
		a.log.Debug("waiting before the next events request", "until", a.eventsWait.retryAt)
		return len(a.outbox.events) == 0
	}

	for len(a.outbox.events) > 0 {
		n := min(len(a.outbox.events), maxEventsPerRequest)
		_, err := a.cp.PostEvents(ctx, &wire.EventsRequest{Events: a.outbox.events[:n]})
		if a.outcome(ctx, &a.eventsWait, start, err, "events", n) == later {
			return false
		}
		a.outbox.dropEvents(n)
	}

	return true
}

// sendSamples sends the queued samples, oldest first, with the status of the
// server in each request. It returns true when the sample queue is empty.
func (a *agent) sendSamples(ctx context.Context, start time.Time) bool {
	if len(a.outbox.samples) == 0 {
		return true
	}
	if a.metricsWait.waiting(start) {
		a.log.Debug("waiting before the next metrics request", "until", a.metricsWait.retryAt)
		return false
	}

	var status *wire.Status
	if a.collector != nil {
		s := cleanStatus(a.collector.Status(ctx))
		status = &s
	}

	for len(a.outbox.samples) > 0 {
		n := min(len(a.outbox.samples), maxSamplesPerRequest)
		reply, err := a.cp.PostMetrics(ctx, &wire.MetricsRequest{
			AgentVersion: truncate(version.Version, maxVersionLen),
			Status:       status,
			Samples:      a.outbox.samples[:n],
		})

		switch a.outcome(ctx, &a.metricsWait, start, err, "samples", n) {
		case later:
			return false
		case sent:
			if len(reply.Rejected) > 0 {
				a.log.Warn("the control plane rejected some samples", "rejected", len(reply.Rejected), "first_reason", reply.Rejected[0].Reason)
			}
			a.setInterval(reply.ReportInterval)
		}
		a.outbox.dropSamples(n)
	}

	return true
}

// outcome applies the rules of the contract (sections 4 to 6) to the result
// of a request that carried n items of what. The waits go into b and count
// from start.
func (a *agent) outcome(ctx context.Context, b *backoff, start time.Time, err error, what string, n int) outcome {
	if err == nil {
		b.failures = 0
		// After the warnings of a failure, say that the data goes again.
		if !b.failingSince.IsZero() {
			a.log.Info("the control plane accepts the requests again", "request", what, "failing_for", start.Sub(b.failingSince).Round(time.Second))
			b.failingSince = time.Time{}
		}
		return sent
	}

	// The time of this report ran out, or the agent stops. That is not a
	// failure of the control plane: the data goes with the next report.
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			a.log.Info("the time for this report is used; the rest goes with the next report", "request", what)
		}
		return later
	}

	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		switch code := statusErr.StatusCode; {
		case code == http.StatusBadRequest:
			b.failures, b.failingSince = 0, time.Time{}
			a.log.Error("the control plane refused the request as not valid; dropping its data", "request", what, "count", n, "reply", statusErr.Body)
			return refused
		case code == http.StatusUnprocessableEntity && what == "samples":
			b.failures, b.failingSince = 0, time.Time{}
			a.log.Warn("the control plane rejected every sample; dropping them", "count", n, "reply", statusErr.Body)
			return refused
		case code == http.StatusUnauthorized:
			b.retryAt = start.Add(unauthorizedWait)
			b.failed(start)
			a.log.Error("the control plane does not accept the token; keeping the data", "request", what, "retry_in", unauthorizedWait)
			return later
		case code == http.StatusTooManyRequests:
			wait := statusErr.RetryAfter
			if wait <= 0 {
				wait = throttledWait
			}
			b.retryAt = start.Add(wait)
			b.failed(start)
			a.log.Warn("the control plane asks the agent to wait; keeping the data", "request", what, "retry_in", wait)
			return later
		}
	}

	// A 5xx, another status or a network error: keep the data and wait longer
	// after each failure.
	b.failures++
	wait := min(firstBackoff<<min(b.failures-1, 4), maxBackoff)
	b.retryAt = start.Add(wait)
	b.failed(start)
	a.log.Warn("the request failed; keeping its data", "request", what, "error", err, "retry_in", wait)

	return later
}

// PostMetrics sends samples and the status (contract section 4).
func (c *Client) PostMetrics(ctx context.Context, req *wire.MetricsRequest) (*wire.MetricsReply, error) {
	var reply wire.MetricsReply
	if err := c.do(ctx, http.MethodPost, "agent/v1/metrics", req, &reply); err != nil {
		return nil, err
	}

	return &reply, nil
}

// PostEvents sends events (contract section 6).
func (c *Client) PostEvents(ctx context.Context, req *wire.EventsRequest) (*wire.EventsReply, error) {
	var reply wire.EventsReply
	if err := c.do(ctx, http.MethodPost, "agent/v1/events", req, &reply); err != nil {
		return nil, err
	}

	return &reply, nil
}
