package harness

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"flex-agent-runtime/internal/agent"
)

// EventConfidence indicates the fidelity of a normalized event.
type EventConfidence string

const (
	ConfidenceHigh     EventConfidence = "high"     // from OTEL (highest fidelity)
	ConfidenceMedium   EventConfidence = "medium"   // from hooks (reliable but coarser)
	ConfidenceDegraded EventConfidence = "degraded" // from session-log (fallback)
)

// NormalizedEvent is an AgentEvent with source provenance and confidence.
type NormalizedEvent struct {
	Event      agent.AgentEvent
	Source     string // "otel", "hook", "session_log"
	Confidence EventConfidence
}

// EventNormalizer reconciles events from multiple sources using the
// three-source priority hierarchy: OTEL > hooks > session-log.
type EventNormalizer struct {
	events []NormalizedEvent
}

// NewEventNormalizer creates a new EventNormalizer.
func NewEventNormalizer() *EventNormalizer {
	return &EventNormalizer{}
}

// AddEvent adds a normalized event from a specific source.
func (n *EventNormalizer) AddEvent(evt agent.AgentEvent, source string) {
	confidence := sourceToConfidence(source)
	n.events = append(n.events, NormalizedEvent{
		Event:      evt,
		Source:     source,
		Confidence: confidence,
	})
}

// Events returns all normalized events in order.
func (n *EventNormalizer) Events() []NormalizedEvent {
	cp := make([]NormalizedEvent, len(n.events))
	copy(cp, n.events)
	return cp
}

// ResolveConflicts applies the source priority hierarchy to deduplicate events.
// When multiple sources report the same event type AND identity fields within
// a time window, the higher-priority source wins. Events with different identity
// fields (e.g., different tool names or call IDs) are never deduplicated.
func (n *EventNormalizer) ResolveConflicts(window time.Duration) []NormalizedEvent {
	if len(n.events) == 0 {
		return nil
	}

	var resolved []NormalizedEvent
	used := make([]bool, len(n.events))

	for i, evt := range n.events {
		if used[i] {
			continue
		}
		// Find conflicting events within the time window
		best := i
		for j := i + 1; j < len(n.events); j++ {
			if used[j] {
				continue
			}
			if n.events[j].Event.Type != evt.Event.Type {
				continue
			}
			// Check identity fields match — different logical events should not be deduped
			if !eventsShareIdentity(n.events[best].Event, n.events[j].Event) {
				continue
			}
			dt := n.events[j].Event.At.Sub(evt.Event.At)
			if dt < 0 {
				dt = -dt
			}
			if dt > window {
				continue
			}
			// Same event type + identity within window — keep higher priority
			if sourcePriority(n.events[j].Source) > sourcePriority(n.events[best].Source) {
				used[best] = true
				best = j
			} else {
				used[j] = true
			}
		}
		resolved = append(resolved, n.events[best])
		used[best] = true
	}

	return resolved
}

// eventsShareIdentity checks whether two events represent the same logical event.
// Events with different tool names, call IDs, or session IDs are distinct.
func eventsShareIdentity(a, b agent.AgentEvent) bool {
	// Tool events: compare tool name and call ID
	if a.ToolName != "" || b.ToolName != "" {
		return a.ToolName == b.ToolName && a.ToolCallID == b.ToolCallID
	}
	// Session events: compare session ID
	if a.SessionID != "" || b.SessionID != "" {
		return a.SessionID == b.SessionID
	}
	// No identity fields — treat as same logical event
	return true
}

func sourceToConfidence(source string) EventConfidence {
	switch source {
	case "otel":
		return ConfidenceHigh
	case "hook":
		return ConfidenceMedium
	case "session_log":
		return ConfidenceDegraded
	default:
		return ConfidenceDegraded
	}
}

func sourcePriority(source string) int {
	switch source {
	case "otel":
		return 3
	case "hook":
		return 2
	case "session_log":
		return 1
	default:
		return 0
	}
}

// --- Assertion helpers ---

// AssertEventSequence verifies that the given event types appear in order
// in the event stream (not necessarily contiguously).
func AssertEventSequence(t *testing.T, events []agent.AgentEvent, types ...agent.AgentEventType) {
	t.Helper()
	idx := 0
	for _, e := range events {
		if idx < len(types) && e.Type == types[idx] {
			idx++
		}
	}
	if idx != len(types) {
		var got []string
		for _, e := range events {
			got = append(got, string(e.Type))
		}
		var want []string
		for _, tp := range types {
			want = append(want, string(tp))
		}
		t.Fatalf("event sequence mismatch: matched %d/%d types\n  want: %v\n  got:  %v",
			idx, len(types), want, got)
	}
}

// AssertNoEventGaps verifies there are no time gaps larger than maxGap
// between consecutive events.
func AssertNoEventGaps(t *testing.T, events []agent.AgentEvent, maxGap time.Duration) {
	t.Helper()
	for i := 1; i < len(events); i++ {
		gap := events[i].At.Sub(events[i-1].At)
		if gap > maxGap {
			t.Fatalf("event gap %v at index %d→%d exceeds max %v (event types: %s→%s)",
				gap, i-1, i, maxGap, events[i-1].Type, events[i].Type)
		}
	}
}

// AssertEventCount checks the number of events of a given type.
func AssertEventCount(t *testing.T, events []agent.AgentEvent, typ agent.AgentEventType, expected int) {
	t.Helper()
	count := 0
	for _, e := range events {
		if e.Type == typ {
			count++
		}
	}
	if count != expected {
		t.Fatalf("expected %d %s events, got %d", expected, typ, count)
	}
}

// AssertStateTransitions verifies that state_change events follow the given sequence.
func AssertStateTransitions(t *testing.T, events []agent.AgentEvent, states ...agent.AgentState) {
	t.Helper()
	var actual []agent.AgentState
	for _, e := range events {
		if e.Type == agent.EventStateChange {
			actual = append(actual, e.State)
		}
	}
	if len(actual) < len(states) {
		t.Fatalf("expected at least %d state transitions, got %d: %v", len(states), len(actual), actual)
	}
	idx := 0
	for _, s := range actual {
		if idx < len(states) && s == states[idx] {
			idx++
		}
	}
	if idx != len(states) {
		t.Fatalf("state transition sequence mismatch: matched %d/%d\n  want: %v\n  got:  %v",
			idx, len(states), states, actual)
	}
}

// AssertNormalizedConfidence checks that all events from a given source
// have the expected confidence level.
func AssertNormalizedConfidence(t *testing.T, events []NormalizedEvent, source string, expectedConfidence EventConfidence) {
	t.Helper()
	for i, e := range events {
		if e.Source == source && e.Confidence != expectedConfidence {
			t.Fatalf("event[%d] source=%s: confidence=%s, want=%s",
				i, source, e.Confidence, expectedConfidence)
		}
	}
}

// AssertConfigPathStable verifies the config directory path is deterministic
// and matches the expected scheme: <dataDir>/configs/<sessionID>/
func AssertConfigPathStable(t *testing.T, configPath, dataDir, sessionID string) {
	t.Helper()
	expected := fmt.Sprintf("%s/configs/%s", dataDir, sessionID)
	// Normalize trailing slashes
	configPath = strings.TrimRight(configPath, "/")
	expected = strings.TrimRight(expected, "/")
	if configPath != expected {
		t.Fatalf("config path mismatch:\n  got:  %s\n  want: %s", configPath, expected)
	}
}

// DumpEventTimeline logs the full event timeline for debugging.
func DumpEventTimeline(t *testing.T, events []agent.AgentEvent) {
	t.Helper()
	t.Logf("=== Event Timeline (%d events) ===", len(events))
	for i, e := range events {
		extra := ""
		if e.ToolName != "" {
			extra += fmt.Sprintf(" tool=%s", e.ToolName)
		}
		if e.State != "" {
			extra += fmt.Sprintf(" state=%s", e.State)
		}
		if e.ErrorMessage != "" {
			extra += fmt.Sprintf(" err=%q", e.ErrorMessage)
		}
		t.Logf("  [%d] %s %s%s", i, e.At.Format("15:04:05.000"), e.Type, extra)
	}
}
