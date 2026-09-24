// Package wire holds the JSON bodies of the FlyWP monitoring agent contract
// v0.2.1: the requests that the agent sends and the replies that it reads.
package wire

import (
	"encoding/json"
	"time"
)

// MetricsRequest is the body of POST /agent/v1/metrics (contract section 4).
type MetricsRequest struct {
	// AgentVersion is the release tag of the agent, exactly (v0.2.0).
	AgentVersion string   `json:"agent_version"`
	Status       *Status  `json:"status,omitempty"`
	Samples      []Sample `json:"samples"`
}

// Status describes the server now. The control plane replaces the stored
// status with it, so the agent always sends all fields.
type Status struct {
	RebootRequired bool `json:"reboot_required"`
	// The waiting updates. nil (JSON null) means "not known", for example
	// without apt-check (contract v0.3.0). A 0 is a real 0.
	UpdatesTotal    *uint64 `json:"updates_total"`
	UpdatesSecurity *uint64 `json:"updates_security"`
	OS              string  `json:"os"`
	Kernel          string  `json:"kernel"`
	UptimeSeconds   uint64  `json:"uptime_seconds"`
	Arch            string  `json:"arch"`
}

// Sample holds the measurements of one minute.
type Sample struct {
	RecordedAt       time.Time `json:"recorded_at"`
	CPUPercent       float64   `json:"cpu_percent"`
	Load1            float64   `json:"load_1"`
	MemoryUsedBytes  uint64    `json:"memory_used_bytes"`
	MemoryTotalBytes uint64    `json:"memory_total_bytes"`
	SwapUsedBytes    uint64    `json:"swap_used_bytes"`
	SwapTotalBytes   uint64    `json:"swap_total_bytes"`
	DiskUsedBytes    uint64    `json:"disk_used_bytes"`
	DiskTotalBytes   uint64    `json:"disk_total_bytes"`
	// NetInBytes and NetOutBytes are the traffic of this minute, not the
	// kernel counters.
	NetInBytes       uint64 `json:"net_in_bytes"`
	NetOutBytes      uint64 `json:"net_out_bytes"`
	NetCountersReset bool   `json:"net_counters_reset"`

	// The peaks within the minute, from the readings each 10 seconds
	// (contract v0.4.0). nil (JSON null) means "not known".
	CPUMaxPercent           *float64 `json:"cpu_max_percent"`
	MemoryUsedMaxBytes      *uint64  `json:"memory_used_max_bytes"`
	SwapUsedMaxBytes        *uint64  `json:"swap_used_max_bytes"`
	NetInMaxBytesPerSecond  *uint64  `json:"net_in_max_bytes_per_second"`
	NetOutMaxBytesPerSecond *uint64  `json:"net_out_max_bytes_per_second"`
}

// MetricsReply is the reply to POST /agent/v1/metrics.
type MetricsReply struct {
	Accepted int        `json:"accepted"`
	Rejected []Rejected `json:"rejected"`
	// ReportInterval is the number of samples for each report from now on.
	ReportInterval int `json:"report_interval"`
}

// Rejected is a sample that the control plane did not store.
type Rejected struct {
	RecordedAt string `json:"recorded_at"`
	Reason     string `json:"reason"`
}

// EventsRequest is the body of POST /agent/v1/events (contract section 6).
type EventsRequest struct {
	Events []Event `json:"events"`
}

// Event names of the contract.
const (
	EventAgentStarted     = "agent.started"
	EventCommandCompleted = "command.completed"
	EventCommandFailed    = "command.failed"
	EventCommandUnknown   = "command.unknown"
)

// Event is a fact that the agent reports. A resend has the same ID, so the
// control plane applies each event one time.
type Event struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CommandID string     `json:"command_id,omitempty"`
	At        time.Time  `json:"at"`
	Data      *EventData `json:"data,omitempty"`
}

// EventData is the result of a command, or the version for agent.started.
type EventData struct {
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// EventsReply is the reply to POST /agent/v1/events.
type EventsReply struct {
	Accepted int `json:"accepted"`
}

// CommandsReply is the reply to GET /agent/v1/commands (contract section 5):
// the open commands of the server, oldest first.
type CommandsReply struct {
	Commands []Command `json:"commands"`
}

// The verbs of the contract. The agent runs no other verb.
const (
	VerbUpdate  = "agent.update"
	VerbRestart = "agent.restart"
)

// Command is a command from the control plane. It comes again on each poll
// until an event finishes it.
type Command struct {
	ID       string          `json:"id"`
	Verb     string          `json:"verb"`
	Args     json.RawMessage `json:"args"`
	IssuedAt time.Time       `json:"issued_at"`
}

// UpdateArgs are the arguments of agent.update. SHA256 is the sha256 of the
// release archive at URL, and Version is its release tag.
type UpdateArgs struct {
	URL     string `json:"url"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}
