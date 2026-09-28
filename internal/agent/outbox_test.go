package agent

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flywp/server-cli/internal/agent/wire"
	"github.com/flywp/server-cli/internal/statefile"
)

func TestOutboxDropsTheOldestSample(t *testing.T) {
	dir := t.TempDir()
	full := make([]wire.Sample, maxSamples)
	for i := range full {
		full[i].CPUPercent = float64(i)
	}
	if err := statefile.Write(filepath.Join(dir, "samples.json"), full); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	o := loadOutbox(dir, slog.New(rec))
	o.addSample(wire.Sample{CPUPercent: maxSamples})
	if len(rec.times("the sample queue is full (24 hours); dropping the oldest sample")) != 1 {
		t.Error("want a warning when the full queue drops a sample")
	}

	if len(o.samples) != maxSamples {
		t.Fatalf("queue holds %d samples, want %d", len(o.samples), maxSamples)
	}
	if o.samples[0].CPUPercent != 1 {
		t.Errorf("oldest sample = %v, want sample 0 dropped", o.samples[0].CPUPercent)
	}
}

func TestOutboxSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	o := loadOutbox(dir, slog.New(&recorder{}))
	o.addSample(wire.Sample{CPUPercent: 1})
	o.addSample(wire.Sample{CPUPercent: 2})
	o.addEvent(wire.Event{ID: "01JBY0000000000000000000AA", Name: wire.EventCommandCompleted, CommandID: "01JBX0000000000000000000AA"})
	o.dropSamples(1)

	again := loadOutbox(dir, slog.New(&recorder{}))
	if len(again.samples) != 1 || again.samples[0].CPUPercent != 2 {
		t.Errorf("samples after a restart = %+v, want the second sample only", again.samples)
	}
	if len(again.events) != 1 || again.events[0].ID != "01JBY0000000000000000000AA" {
		t.Errorf("events after a restart = %+v, want the event with its id", again.events)
	}
}

func TestOutboxKeepsOnlyTheLastAgentStarted(t *testing.T) {
	o := loadOutbox(t.TempDir(), slog.New(&recorder{}))
	o.addEvent(wire.Event{ID: "a", Name: wire.EventAgentStarted})
	o.addEvent(wire.Event{ID: "b", Name: wire.EventCommandCompleted})
	o.addEvent(wire.Event{ID: "c", Name: wire.EventAgentStarted})

	var ids []string
	for _, e := range o.events {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "b,c" {
		t.Errorf("events = %v, want b,c: an older agent.started goes", ids)
	}
}

func TestOutboxLimitsEvents(t *testing.T) {
	dir := t.TempDir()
	full := make([]wire.Event, maxEvents)
	for i := range full {
		full[i] = wire.Event{ID: fmt.Sprint(i), Name: wire.EventCommandFailed}
	}
	if err := statefile.Write(filepath.Join(dir, "events.json"), full); err != nil {
		t.Fatal(err)
	}

	o := loadOutbox(dir, slog.New(&recorder{}))
	o.addEvent(wire.Event{ID: "new", Name: wire.EventCommandFailed})
	if len(o.events) != maxEvents || o.events[0].ID != "1" || o.events[maxEvents-1].ID != "new" {
		t.Errorf("queue holds %d events from %s to %s, want %d with event 0 dropped", len(o.events), o.events[0].ID, o.events[len(o.events)-1].ID, maxEvents)
	}
}

func TestOutboxWithAQueueThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "samples.json"), []byte("[{"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	o := loadOutbox(dir, slog.New(rec))
	if len(o.samples) != 0 {
		t.Errorf("samples = %d, want an empty queue", len(o.samples))
	}
	if len(rec.times("dropping a queue that cannot be read; the file is kept as samples.json.corrupt")) != 1 {
		t.Error("want an error log line for the queue that cannot be read")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "samples.json.corrupt")); err != nil || string(data) != "[{" {
		t.Errorf("samples.json.corrupt = %q, %v; want the file kept for a person to examine", data, err)
	}
}

func TestCleanSample(t *testing.T) {
	recorded := time.Date(2026, 9, 22, 10, 0, 17, 123456789, time.FixedZone("x", 3600))
	tests := []struct {
		cpu, load         float64
		wantCPU, wantLoad float64
	}{
		{12.5, 0.4, 12.5, 0.4},
		{150, 2e6, 100, maxLoad},
		{-1, -3, 0, 0},
		{math.NaN(), math.NaN(), 0, 0},
	}

	for _, tt := range tests {
		got := cleanSample(wire.Sample{RecordedAt: recorded, CPUPercent: tt.cpu, Load1: tt.load})
		if got.CPUPercent != tt.wantCPU || got.Load1 != tt.wantLoad {
			t.Errorf("cleanSample(cpu %v, load %v) = %v, %v; want %v, %v", tt.cpu, tt.load, got.CPUPercent, got.Load1, tt.wantCPU, tt.wantLoad)
		}
		if want := time.Date(2026, 9, 22, 9, 0, 17, 0, time.UTC); !got.RecordedAt.Equal(want) || got.RecordedAt.Location() != time.UTC {
			t.Errorf("recorded_at = %s, want %s in UTC", got.RecordedAt, want)
		}
	}
}

func TestCleanTextFields(t *testing.T) {
	long := strings.Repeat("é", 300)

	s := cleanStatus(wire.Status{OS: long, Kernel: long, Arch: strings.Repeat("x", 20)})
	if n := len([]rune(s.OS)); n != maxStatusTextLen {
		t.Errorf("os has %d characters, want %d", n, maxStatusTextLen)
	}
	if n := len([]rune(s.Kernel)); n != maxStatusTextLen {
		t.Errorf("kernel has %d characters, want %d", n, maxStatusTextLen)
	}
	if len(s.Arch) != maxArchLen {
		t.Errorf("arch has %d characters, want %d", len(s.Arch), maxArchLen)
	}

	e := cleanEvent(wire.Event{Name: strings.Repeat("n", 70), Data: &wire.EventData{Version: strings.Repeat("v", 40), Error: strings.Repeat("e", 2500)}})
	if len(e.Name) != maxEventNameLen || len(e.Data.Version) != maxVersionLen || len(e.Data.Error) != maxErrorLen {
		t.Errorf("event lengths = %d, %d, %d; want %d, %d, %d", len(e.Name), len(e.Data.Version), len(e.Data.Error), maxEventNameLen, maxVersionLen, maxErrorLen)
	}
}

func TestCleanKeepsIntegersInTheRangeOfPHP(t *testing.T) {
	s := cleanSample(wire.Sample{NetInBytes: math.MaxUint64, MemoryTotalBytes: 1 << 63, DiskUsedBytes: 42})
	if s.NetInBytes != math.MaxInt64 || s.MemoryTotalBytes != math.MaxInt64 || s.DiskUsedBytes != 42 {
		t.Errorf("cleanSample() = %d, %d, %d; want %d, %d, 42", s.NetInBytes, s.MemoryTotalBytes, s.DiskUsedBytes, uint64(math.MaxInt64), uint64(math.MaxInt64))
	}

	st := cleanStatus(wire.Status{UpdatesTotal: math.MaxUint64, UpdatesSecurity: 1 << 63, UptimeSeconds: math.MaxUint64})
	if st.UpdatesTotal != math.MaxInt64 || st.UpdatesSecurity != math.MaxInt64 || st.UptimeSeconds != math.MaxInt64 {
		t.Errorf("cleanStatus() = %+v, want each count at most %d", st, uint64(math.MaxInt64))
	}
}

func TestCleanEventDropsACommandIDThatIsNotAULID(t *testing.T) {
	if e := cleanEvent(wire.Event{CommandID: "not-a-ulid"}); e.CommandID != "" {
		t.Errorf("command_id = %q, want it removed", e.CommandID)
	}
	if e := cleanEvent(wire.Event{CommandID: "01JBX0000000000000000000AA"}); e.CommandID != "01JBX0000000000000000000AA" {
		t.Errorf("command_id = %q, want the ULID kept", e.CommandID)
	}
}
