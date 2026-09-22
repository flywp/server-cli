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

	// The reply funcs get the number of the call, from 0.
	metricsReply func(call int, req *wire.MetricsRequest) (*wire.MetricsReply, error)
	eventsReply  func(call int, req *wire.EventsRequest) (*wire.EventsReply, error)
}

func (f *fakeCP) PostMetrics(_ context.Context, req *wire.MetricsRequest) (*wire.MetricsReply, error) {
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
	status wire.Status
	err    error
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
