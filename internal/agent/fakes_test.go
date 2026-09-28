package agent

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
)

// fakeCP is a control plane in memory. It keeps a copy of each request and
// the time of the request, and it answers with the reply funcs. A nil func
// accepts everything.
type fakeCP struct {
	mu sync.Mutex

	metrics   []wire.MetricsRequest
	metricsAt []time.Time
	events    []wire.EventsRequest
	eventsAt  []time.Time
	pollAt    []time.Time

	// latency is the time of each metrics request. The request ends early
	// when its context ends, like a real HTTP request.
	latency time.Duration

	// The reply funcs get the number of the call, from 0.
	metricsReply func(call int, req *wire.MetricsRequest) (*wire.MetricsReply, error)
	eventsReply  func(call int, req *wire.EventsRequest) (*wire.EventsReply, error)
	// commands are the open commands of each poll. A command stays open
	// until an event with its id arrives, unless keepOpen is true.
	commands []wire.Command
	keepOpen bool
}

func (f *fakeCP) PollCommands(context.Context) (*wire.CommandsReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pollAt = append(f.pollAt, time.Now())

	var open []wire.Command
	for _, c := range f.commands {
		if f.keepOpen || len(f.resultsLocked(c.ID)) == 0 {
			open = append(open, c)
		}
	}
	return &wire.CommandsReply{Commands: open}, nil
}

// results returns the events that the agent sent for the command id.
func (f *fakeCP) results(id string) []wire.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resultsLocked(id)
}

func (f *fakeCP) resultsLocked(id string) []wire.Event {
	var out []wire.Event
	for _, r := range f.events {
		for _, e := range r.Events {
			if e.CommandID == id {
				out = append(out, e)
			}
		}
	}
	return out
}

func (f *fakeCP) PostMetrics(ctx context.Context, req *wire.MetricsRequest) (*wire.MetricsReply, error) {
	if f.latency > 0 {
		select {
		case <-time.After(f.latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Copy the samples: the agent reuses the memory of its queue.
	c := *req
	c.Samples = slices.Clone(req.Samples)
	f.metrics = append(f.metrics, c)
	f.metricsAt = append(f.metricsAt, time.Now())

	if f.metricsReply == nil {
		return &wire.MetricsReply{Accepted: len(req.Samples)}, nil
	}
	return f.metricsReply(len(f.metrics)-1, &c)
}

func (f *fakeCP) PostEvents(_ context.Context, req *wire.EventsRequest) (*wire.EventsReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := wire.EventsRequest{Events: slices.Clone(req.Events)}
	f.events = append(f.events, c)
	f.eventsAt = append(f.eventsAt, time.Now())

	if f.eventsReply == nil {
		return &wire.EventsReply{Accepted: len(req.Events)}, nil
	}
	return f.eventsReply(len(f.events)-1, &c)
}

// sampleCounts returns the number of samples in each metrics request.
func (f *fakeCP) sampleCounts() []int {
	f.mu.Lock()
	defer f.mu.Unlock()

	var n []int
	for _, r := range f.metrics {
		n = append(n, len(r.Samples))
	}
	return n
}

// fakeCollector returns samples whose CPU value counts the samples: 1, 2, 3...
type fakeCollector struct {
	mu     sync.Mutex
	n      int
	reads  []time.Time
	status wire.Status
	err    error
	// readTime is the time that each reading takes.
	readTime time.Duration
}

func (c *fakeCollector) Read(now time.Time) {
	c.mu.Lock()
	c.reads = append(c.reads, now)
	d := c.readTime
	c.mu.Unlock()
	time.Sleep(d)
}

// readTimes returns the times of the readings between the ticks.
func (c *fakeCollector) readTimes() []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reads)
}

func (c *fakeCollector) Sample(time.Time) (wire.Sample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.err != nil {
		return wire.Sample{}, c.err
	}
	c.n++
	return wire.Sample{CPUPercent: float64(c.n), MemoryTotalBytes: 1 << 30}, nil
}

func (c *fakeCollector) Status(context.Context) wire.Status {
	return c.status
}
