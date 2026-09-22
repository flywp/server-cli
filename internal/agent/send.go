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
	// later: keep the data, and send nothing until retryAt.
	later
)

// send sends the events and then the samples. With report false, it sends
// only the events.
func (a *agent) send(ctx context.Context, report bool) {
	if wait := time.Until(a.retryAt); wait > 0 {
		a.log.Debug("waiting before the next send", "wait", wait)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, sendBudget)
	defer cancel()

	if a.sendEvents(ctx) && report {
		a.sendSamples(ctx)
	}
}

// sendEvents sends the queued events, oldest first. It returns false when the
// agent must send nothing more now.
func (a *agent) sendEvents(ctx context.Context) bool {
	for len(a.outbox.events) > 0 {
		n := min(len(a.outbox.events), maxEventsPerRequest)
		_, err := a.cp.PostEvents(ctx, &wire.EventsRequest{Events: a.outbox.events[:n]})
		if a.outcome(err, "events", n) == later {
			return false
		}
		a.outbox.dropEvents(n)
	}

	return true
}

// sendSamples sends the queued samples, oldest first, with the status of the
// server in each request.
func (a *agent) sendSamples(ctx context.Context) {
	if len(a.outbox.samples) == 0 {
		return
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

		switch a.outcome(err, "samples", n) {
		case later:
			return
		case sent:
			if len(reply.Rejected) > 0 {
				a.log.Warn("the control plane rejected some samples", "rejected", len(reply.Rejected), "first_reason", reply.Rejected[0].Reason)
			}
			a.setInterval(reply.ReportInterval)
		}
		a.outbox.dropSamples(n)
	}
}

// outcome applies the rules of the contract (sections 4 and 6) to the result
// of a request that carried n items of what.
func (a *agent) outcome(err error, what string, n int) outcome {
	if err == nil {
		a.failures = 0
		return sent
	}

	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		switch code := statusErr.StatusCode; {
		case code == http.StatusBadRequest:
			a.failures = 0
			a.log.Error("the control plane refused the "+what+" as not valid; dropping them", "count", n)
			return refused
		case code == http.StatusUnprocessableEntity && what == "samples":
			a.failures = 0
			a.log.Warn("the control plane rejected every sample; dropping them", "count", n)
			return refused
		case code == http.StatusUnauthorized:
			a.retryAt = time.Now().Add(unauthorizedWait)
			a.log.Error("the control plane does not accept the token; keeping the data", "retry_in", unauthorizedWait)
			return later
		case code == http.StatusTooManyRequests:
			wait := statusErr.RetryAfter
			if wait <= 0 {
				wait = throttledWait
			}
			a.retryAt = time.Now().Add(wait)
			a.log.Warn("the control plane asks the agent to wait; keeping the data", "retry_in", wait)
			return later
		}
	}

	// A 5xx, another status or a network error: keep the data and wait longer
	// after each failure.
	a.failures++
	wait := min(firstBackoff<<min(a.failures-1, 4), maxBackoff)
	a.retryAt = time.Now().Add(wait)
	a.log.Warn("sending "+what+" failed; keeping them", "error", err, "retry_in", wait)

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
