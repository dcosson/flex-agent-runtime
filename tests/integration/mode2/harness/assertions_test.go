package harness

import (
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
)

func TestResolveConflicts_Empty(t *testing.T) {
	n := NewEventNormalizer()
	resolved := n.ResolveConflicts(200 * time.Millisecond)
	if len(resolved) != 0 {
		t.Fatalf("expected 0 resolved, got %d", len(resolved))
	}
}

func TestResolveConflicts_SingleSource(t *testing.T) {
	n := NewEventNormalizer()
	now := time.Now()
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", At: now}, "otel")
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolCompleted, ToolName: "read", At: now.Add(100 * time.Millisecond)}, "otel")

	resolved := n.ResolveConflicts(200 * time.Millisecond)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved, got %d", len(resolved))
	}
}

func TestResolveConflicts_OTELWinsOverHook(t *testing.T) {
	n := NewEventNormalizer()
	now := time.Now()
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now}, "hook")
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now.Add(50 * time.Millisecond)}, "otel")

	resolved := n.ResolveConflicts(200 * time.Millisecond)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved, got %d", len(resolved))
	}
	if resolved[0].Source != "otel" {
		t.Fatalf("expected otel to win, got %q", resolved[0].Source)
	}
}

func TestResolveConflicts_DifferentToolsNotDeduped(t *testing.T) {
	n := NewEventNormalizer()
	now := time.Now()
	// Two different tool_started events within the same window — should NOT be deduped
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now}, "otel")
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "write", ToolCallID: "c2", At: now.Add(50 * time.Millisecond)}, "hook")

	resolved := n.ResolveConflicts(200 * time.Millisecond)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved (different tools), got %d", len(resolved))
	}
}

func TestResolveConflicts_OutsideWindow(t *testing.T) {
	n := NewEventNormalizer()
	now := time.Now()
	// Same event type and identity but outside the time window
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now}, "hook")
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now.Add(500 * time.Millisecond)}, "otel")

	resolved := n.ResolveConflicts(100 * time.Millisecond)
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved (outside window), got %d", len(resolved))
	}
}

func TestResolveConflicts_AllSamePriority(t *testing.T) {
	n := NewEventNormalizer()
	now := time.Now()
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now}, "otel")
	n.AddEvent(agent.AgentEvent{Type: agent.EventToolStarted, ToolName: "read", ToolCallID: "c1", At: now.Add(10 * time.Millisecond)}, "otel")

	resolved := n.ResolveConflicts(200 * time.Millisecond)
	if len(resolved) != 1 {
		t.Fatalf("expected 1 resolved (same priority dedup), got %d", len(resolved))
	}
}

func TestAssertEventSequence_Matching(t *testing.T) {
	events := []agent.AgentEvent{
		{Type: agent.EventSessionStarted},
		{Type: agent.EventToolStarted},
		{Type: agent.EventToolCompleted},
		{Type: agent.EventSessionEnded},
	}
	// Should not fail
	AssertEventSequence(t, events, agent.EventSessionStarted, agent.EventToolCompleted, agent.EventSessionEnded)
}

func TestAssertEventSequence_NonContiguous(t *testing.T) {
	events := []agent.AgentEvent{
		{Type: agent.EventSessionStarted},
		{Type: agent.EventToolStarted},
		{Type: agent.EventAgentMessageCompleted},
		{Type: agent.EventToolCompleted},
		{Type: agent.EventSessionEnded},
	}
	// Subsequence match, not contiguous
	AssertEventSequence(t, events, agent.EventSessionStarted, agent.EventToolCompleted)
}

func TestConfidenceLevels(t *testing.T) {
	tests := []struct {
		source     string
		confidence EventConfidence
	}{
		{"otel", ConfidenceHigh},
		{"hook", ConfidenceMedium},
		{"session_log", ConfidenceDegraded},
		{"unknown", ConfidenceDegraded},
	}
	for _, tc := range tests {
		t.Run(tc.source, func(t *testing.T) {
			got := sourceToConfidence(tc.source)
			if got != tc.confidence {
				t.Fatalf("source=%q: got %q, want %q", tc.source, got, tc.confidence)
			}
		})
	}
}

func TestEventsShareIdentity(t *testing.T) {
	// Same tool + call ID → shared identity
	a := agent.AgentEvent{ToolName: "read", ToolCallID: "c1"}
	b := agent.AgentEvent{ToolName: "read", ToolCallID: "c1"}
	if !eventsShareIdentity(a, b) {
		t.Fatal("expected shared identity for same tool+callID")
	}

	// Different tool → different identity
	c := agent.AgentEvent{ToolName: "write", ToolCallID: "c2"}
	if eventsShareIdentity(a, c) {
		t.Fatal("expected different identity for different tools")
	}

	// No identity fields → shared
	d := agent.AgentEvent{}
	e := agent.AgentEvent{}
	if !eventsShareIdentity(d, e) {
		t.Fatal("expected shared identity for events with no identity fields")
	}
}
