package agent

import (
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/statefile"
)

const (
	// maxSamples is 24 hours of samples (contract section 4). Above it, the
	// oldest sample goes first.
	maxSamples = 1440
	// maxEvents limits the event queue, for example when a crash loop adds
	// events while the control plane is down.
	maxEvents = 1000
)

// outbox keeps the samples and the events that the control plane has not
// accepted yet. Each change goes to disk, so a restart or a reboot loses
// nothing.
type outbox struct {
	dir     string
	log     *slog.Logger
	samples []wire.Sample
	events  []wire.Event
}

func loadOutbox(dir string, log *slog.Logger) *outbox {
	o := &outbox{dir: dir, log: log}
	o.samples = loadQueue[wire.Sample](o, "samples.json")
	o.events = loadQueue[wire.Event](o, "events.json")
	return o
}

// loadQueue reads a queue file. A file that cannot be read gives an empty
// queue: to stop would only make systemd start the agent again.
func loadQueue[T any](o *outbox, name string) []T {
	var q []T
	err := statefile.Read(filepath.Join(o.dir, name), &q)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		o.log.Error("dropping a queue that cannot be read", "file", name, "error", err)
		return nil
	}

	return q
}

func (o *outbox) addSample(s wire.Sample) {
	o.samples = append(o.samples, s)
	if over := len(o.samples) - maxSamples; over > 0 {
		o.samples = slices.Delete(o.samples, 0, over)
	}
	o.save("samples.json", o.samples)
}

func (o *outbox) addEvent(e wire.Event) {
	// The control plane records nothing for agent.started, so only the
	// last one is useful.
	if e.Name == wire.EventAgentStarted {
		o.events = slices.DeleteFunc(o.events, func(q wire.Event) bool { return q.Name == wire.EventAgentStarted })
	}

	o.events = append(o.events, e)
	if over := len(o.events) - maxEvents; over > 0 {
		o.events = slices.Delete(o.events, 0, over)
	}
	o.save("events.json", o.events)
}

// dropSamples removes the n oldest samples.
func (o *outbox) dropSamples(n int) {
	o.samples = slices.Delete(o.samples, 0, n)
	o.save("samples.json", o.samples)
}

// dropEvents removes the n oldest events.
func (o *outbox) dropEvents(n int) {
	o.events = slices.Delete(o.events, 0, n)
	o.save("events.json", o.events)
}

// save writes a queue to disk. When the write fails, the queue stays in memory
// and goes to disk with the next change.
func (o *outbox) save(name string, q any) {
	if err := statefile.Write(filepath.Join(o.dir, name), q); err != nil {
		o.log.Error("saving a queue", "file", name, "error", err)
	}
}
