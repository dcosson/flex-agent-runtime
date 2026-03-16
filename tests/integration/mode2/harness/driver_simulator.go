package harness

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"h2-agent-runtime/internal/termmux/monitor"
)

// ReplayMode controls how inter-event delays are handled.
type ReplayMode int

const (
	// ReplayFastForward replays events with no inter-event delays (CI default).
	ReplayFastForward ReplayMode = iota
	// ReplayRealTime replays events at original recorded timestamps.
	ReplayRealTime
)

// ReplayEntry is a single line in a replay script (JSON Lines format).
type ReplayEntry struct {
	Timestamp time.Time       `json:"ts"`
	Source    string          `json:"source"` // "pty", "otel", "hook"
	Data      json.RawMessage `json:"data"`
}

// PTYData holds decoded PTY output bytes.
type PTYData struct {
	Bytes []byte
}

// OTELData holds a structured OTEL span/event.
type OTELData struct {
	Span  string         `json:"span"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// HookData holds a hook callback payload.
type HookData struct {
	Event     string `json:"event"`
	SessionID string `json:"session_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

// DeterministicDriverSimulator replays a recorded driver session,
// emitting PTY output, OTEL spans, and hook events at recorded timestamps.
// It exercises the real event normalization pipeline without a live driver.
type DeterministicDriverSimulator struct {
	entries []ReplayEntry
	mode    ReplayMode

	// Callbacks for each source type
	OnPTYOutput func(data []byte)
	OnOTELSpan  func(span OTELData)
	OnHookEvent func(hook HookData)

	// MonitorSubmit allows the simulator to inject events into an AgentMonitor.
	MonitorSubmit func(evt monitor.AgentEvent)
}

// NewDeterministicDriverSimulator creates a simulator from a replay script.
func NewDeterministicDriverSimulator(entries []ReplayEntry, mode ReplayMode) *DeterministicDriverSimulator {
	// Sort entries by timestamp for deterministic replay order
	sorted := make([]ReplayEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	return &DeterministicDriverSimulator{
		entries: sorted,
		mode:    mode,
	}
}

// LoadReplayScript loads a replay script from a JSON Lines file.
func LoadReplayScript(path string) ([]ReplayEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read replay script: %w", err)
	}
	return ParseReplayScript(data)
}

// ParseReplayScript parses replay entries from JSON Lines bytes.
func ParseReplayScript(data []byte) ([]ReplayEntry, error) {
	var entries []ReplayEntry
	dec := json.NewDecoder(jsonLinesReader(data))
	for dec.More() {
		var entry ReplayEntry
		if err := dec.Decode(&entry); err != nil {
			return nil, fmt.Errorf("decode replay entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Run replays all entries, calling the appropriate callbacks.
// In fast-forward mode, events are dispatched immediately.
// In real-time mode, inter-event delays are preserved.
func (s *DeterministicDriverSimulator) Run() error {
	var lastTS time.Time
	for i, entry := range s.entries {
		// Apply timing delay in real-time mode
		if s.mode == ReplayRealTime && i > 0 && !lastTS.IsZero() {
			delay := entry.Timestamp.Sub(lastTS)
			if delay > 0 {
				time.Sleep(delay)
			}
		}
		lastTS = entry.Timestamp

		if err := s.dispatchEntry(entry); err != nil {
			return fmt.Errorf("dispatch entry %d: %w", i, err)
		}
	}
	return nil
}

func (s *DeterministicDriverSimulator) dispatchEntry(entry ReplayEntry) error {
	switch entry.Source {
	case "pty":
		return s.dispatchPTY(entry)
	case "otel":
		return s.dispatchOTEL(entry)
	case "hook":
		return s.dispatchHook(entry)
	default:
		return fmt.Errorf("unknown source type: %q", entry.Source)
	}
}

func (s *DeterministicDriverSimulator) dispatchPTY(entry ReplayEntry) error {
	// PTY data is base64-encoded in the replay script
	var b64 string
	if err := json.Unmarshal(entry.Data, &b64); err != nil {
		return fmt.Errorf("decode pty data: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("base64 decode pty data: %w", err)
	}
	if s.OnPTYOutput != nil {
		s.OnPTYOutput(data)
	}
	return nil
}

func (s *DeterministicDriverSimulator) dispatchOTEL(entry ReplayEntry) error {
	var otel OTELData
	if err := json.Unmarshal(entry.Data, &otel); err != nil {
		return fmt.Errorf("decode otel data: %w", err)
	}
	if s.OnOTELSpan != nil {
		s.OnOTELSpan(otel)
	}

	// Also inject into the monitor if configured
	if s.MonitorSubmit != nil {
		evt := otelToMonitorEvent(otel, entry.Timestamp)
		if evt.Type != "" {
			s.MonitorSubmit(evt)
		}
	}
	return nil
}

func (s *DeterministicDriverSimulator) dispatchHook(entry ReplayEntry) error {
	var hook HookData
	if err := json.Unmarshal(entry.Data, &hook); err != nil {
		return fmt.Errorf("decode hook data: %w", err)
	}
	if s.OnHookEvent != nil {
		s.OnHookEvent(hook)
	}

	// Also inject into the monitor if configured
	if s.MonitorSubmit != nil {
		evt := hookToMonitorEvent(hook, entry.Timestamp)
		if evt.Type != "" {
			s.MonitorSubmit(evt)
		}
	}
	return nil
}

// otelToMonitorEvent converts an OTEL span to a monitor event.
func otelToMonitorEvent(otel OTELData, ts time.Time) monitor.AgentEvent {
	switch otel.Span {
	case "session_started":
		sid, _ := otel.Attrs["session_id"].(string)
		return monitor.AgentEvent{
			Type:      monitor.EventSessionStarted,
			Timestamp: ts,
			Data:      monitor.SessionStartedData{SessionID: sid},
		}
	case "session_ended":
		reason, _ := otel.Attrs["reason"].(string)
		return monitor.AgentEvent{
			Type:      monitor.EventSessionEnded,
			Timestamp: ts,
			Data:      monitor.SessionEndedData{Reason: reason},
		}
	case "turn_completed":
		return monitor.AgentEvent{
			Type:      monitor.EventTurnCompleted,
			Timestamp: ts,
			Data:      turnCompletedFromAttrs(otel.Attrs),
		}
	case "tool_started":
		name, _ := otel.Attrs["tool_name"].(string)
		callID, _ := otel.Attrs["call_id"].(string)
		return monitor.AgentEvent{
			Type:      monitor.EventToolStarted,
			Timestamp: ts,
			Data:      monitor.ToolStartedData{ToolName: name, CallID: callID},
		}
	case "tool_completed":
		name, _ := otel.Attrs["tool_name"].(string)
		callID, _ := otel.Attrs["call_id"].(string)
		return monitor.AgentEvent{
			Type:      monitor.EventToolCompleted,
			Timestamp: ts,
			Data:      monitor.ToolCompletedData{ToolName: name, CallID: callID, Success: true},
		}
	default:
		return monitor.AgentEvent{}
	}
}

// hookToMonitorEvent converts a hook callback to a monitor event.
func hookToMonitorEvent(hook HookData, ts time.Time) monitor.AgentEvent {
	switch hook.Event {
	case "session_started":
		return monitor.AgentEvent{
			Type:      monitor.EventSessionStarted,
			Timestamp: ts,
			Data:      monitor.SessionStartedData{SessionID: hook.SessionID},
		}
	case "session_ended":
		return monitor.AgentEvent{
			Type:      monitor.EventSessionEnded,
			Timestamp: ts,
			Data:      monitor.SessionEndedData{Reason: "hook"},
		}
	case "idle":
		return monitor.AgentEvent{
			Type:      monitor.EventStateChange,
			Timestamp: ts,
			Data:      monitor.StateChangeData{State: monitor.StateIdle},
		}
	case "tool_started":
		return monitor.AgentEvent{
			Type:      monitor.EventToolStarted,
			Timestamp: ts,
			Data:      monitor.ToolStartedData{ToolName: hook.ToolName, CallID: hook.CallID},
		}
	case "tool_completed":
		return monitor.AgentEvent{
			Type:      monitor.EventToolCompleted,
			Timestamp: ts,
			Data:      monitor.ToolCompletedData{ToolName: hook.ToolName, CallID: hook.CallID, Success: true},
		}
	default:
		return monitor.AgentEvent{}
	}
}

func turnCompletedFromAttrs(attrs map[string]any) monitor.TurnCompletedData {
	d := monitor.TurnCompletedData{}
	if v, ok := attrs["input_tokens"].(float64); ok {
		d.InputTokens = int64(v)
	}
	if v, ok := attrs["output_tokens"].(float64); ok {
		d.OutputTokens = int64(v)
	}
	if v, ok := attrs["cost_usd"].(float64); ok {
		d.CostUSD = v
	}
	return d
}

// jsonLinesReader wraps bytes for json.Decoder compatibility with JSONL format.
// It replaces newlines with whitespace to allow the decoder to read multiple objects.
func jsonLinesReader(data []byte) *jsonLinesBuffer {
	return &jsonLinesBuffer{data: data}
}

type jsonLinesBuffer struct {
	data []byte
	pos  int
}

func (r *jsonLinesBuffer) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
