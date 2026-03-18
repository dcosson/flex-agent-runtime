package common

import (
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
)

// EventTypeSequence extracts the event type sequence from a list of agent events.
func EventTypeSequence(events []agent.AgentEvent) []agent.AgentEventType {
	types := make([]agent.AgentEventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// AssertEventTypeParity checks that two event sequences have the same event types in order.
func AssertEventTypeParity(t *testing.T, label string, expected, actual []agent.AgentEventType) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Errorf("%s: event count mismatch: expected %d, got %d", label, len(expected), len(actual))
		return
	}
	for i := range expected {
		if expected[i] != actual[i] {
			t.Errorf("%s: event[%d] type mismatch: expected %q, got %q", label, i, expected[i], actual[i])
		}
	}
}

// CountEventType counts occurrences of a specific event type.
func CountEventType(events []agent.AgentEvent, typ agent.AgentEventType) int {
	count := 0
	for _, e := range events {
		if e.Type == typ {
			count++
		}
	}
	return count
}
