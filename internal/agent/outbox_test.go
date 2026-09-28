package agent

import (
	"encoding/json"
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

	total, security := uint64(math.MaxUint64), uint64(1<<63)
	st := cleanStatus(wire.Status{UpdatesTotal: &total, UpdatesSecurity: &security, UptimeSeconds: math.MaxUint64})
	if *st.UpdatesTotal != math.MaxInt64 || *st.UpdatesSecurity != math.MaxInt64 || st.UptimeSeconds != math.MaxInt64 {
		t.Errorf("cleanStatus() = %+v, want each count at most %d", st, uint64(math.MaxInt64))
	}

	// Unknown counts stay unknown, and go on the wire as null.
	st = cleanStatus(wire.Status{})
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"updates_total":null`) || !strings.Contains(string(data), `"updates_security":null`) {
		t.Errorf("status JSON = %s, want null update counts", data)
	}
}

func TestCleanPeaks(t *testing.T) {
	cpu, big := 120.0, uint64(math.MaxUint64)
	s := cleanSample(wire.Sample{CPUMaxPercent: &cpu, MemoryUsedMaxBytes: &big, NetInMaxBytesPerSecond: &big})
	if *s.CPUMaxPercent != 100 || *s.MemoryUsedMaxBytes != math.MaxInt64 || *s.NetInMaxBytesPerSecond != math.MaxInt64 {
		t.Errorf("cleanSample() = %v, %d, %d; want 100 and the largest PHP integer", *s.CPUMaxPercent, *s.MemoryUsedMaxBytes, *s.NetInMaxBytesPerSecond)
	}
	if cpu != 120 {
		t.Error("cleanSample() changed the value of the caller")
	}
	if s.SwapUsedMaxBytes != nil || s.NetOutMaxBytesPerSecond != nil {
		t.Error("cleanSample() gave a value to a peak that is not known")
	}
}

// A sample that an older agent queued has no peaks. After an update, the new
// agent sends them as null: not known.
func TestQueuedSampleOfAnOlderAgentSendsNullPeaks(t *testing.T) {
	dir := t.TempDir()
	old := `[{"recorded_at":"2026-09-24T10:00:17Z","cpu_percent":12.5,"load_1":0.4,"memory_used_bytes":1,"memory_total_bytes":2,` +
		`"swap_used_bytes":0,"swap_total_bytes":0,"disk_used_bytes":1,"disk_total_bytes":2,"net_in_bytes":5,"net_out_bytes":6,"net_counters_reset":false}]`
	if err := os.WriteFile(filepath.Join(dir, "samples.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	o := loadOutbox(dir, slog.New(slog.DiscardHandler))
	if len(o.samples) != 1 {
		t.Fatalf("samples = %d, want the queued sample", len(o.samples))
	}
	data, err := json.Marshal(o.samples[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cpu_max_percent", "memory_used_max_bytes", "swap_used_max_bytes", "net_in_max_bytes_per_second", "net_out_max_bytes_per_second"} {
		if !strings.Contains(string(data), `"`+field+`":null`) {
			t.Errorf("sample JSON = %s, want %s as null", data, field)
		}
	}
}

func TestCleanDockerStatus(t *testing.T) {
	zero, many, four := 0, 5000, 4
	status, version := "paused", strings.Repeat("9", 40)
	s := cleanStatus(wire.Status{CPUCount: &zero, DockerStatus: &status, DockerVersion: &version})
	if s.CPUCount != nil || s.DockerStatus != nil || s.DockerVersion == nil || len(*s.DockerVersion) != maxVersionLen {
		t.Errorf("cleanStatus() = %v, %v, %v; want null, null and %d characters", s.CPUCount, s.DockerStatus, s.DockerVersion, maxVersionLen)
	}
	if s := cleanStatus(wire.Status{CPUCount: &many}); s.CPUCount != nil {
		t.Errorf("cpu_count = %d, want null above %d", *s.CPUCount, maxCPUCount)
	}

	running := wire.DockerRunning
	s = cleanStatus(wire.Status{CPUCount: &four, DockerStatus: &running})
	if s.CPUCount == nil || *s.CPUCount != 4 || s.DockerStatus == nil || *s.DockerStatus != wire.DockerRunning {
		t.Errorf("cleanStatus() = %v, %v; want 4 and running", s.CPUCount, s.DockerStatus)
	}

	// Not known is null on the wire.
	data, err := json.Marshal(cleanStatus(wire.Status{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cpu_count", "docker_status", "docker_version"} {
		if !strings.Contains(string(data), `"`+field+`":null`) {
			t.Errorf("status JSON = %s, want %s as null", data, field)
		}
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
