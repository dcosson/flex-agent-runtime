package eventstore

import (
	"path/filepath"
	"testing"
	"time"

	"h2-agent-runtime/internal/termmux/monitor"
)

func TestStore_AppendAndReadAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	evt1 := monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now().Truncate(time.Millisecond),
	}
	evt2 := monitor.AgentEvent{
		Type:      monitor.EventToolStarted,
		Timestamp: time.Now().Truncate(time.Millisecond),
	}

	if err := s.Append(evt1); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := s.Append(evt2); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	events, err := s.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != monitor.EventSessionStarted {
		t.Errorf("event 0: expected session_started, got %s", events[0].Type)
	}
	if events[1].Type != monitor.EventToolStarted {
		t.Errorf("event 1: expected tool_started, got %s", events[1].Type)
	}
}

func TestStore_AppendAfterClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s.Close()

	err = s.Append(monitor.AgentEvent{Type: monitor.EventSessionStarted})
	if err == nil {
		t.Error("expected error on append after close")
	}
}

func TestStore_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	s.Close()
	// Double close should not panic
	s.Close()
}

func TestStore_ReadAllEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	events, err := s.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll empty: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestStore_ReadAllTypedData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Write events with typed data
	now := time.Now().Truncate(time.Millisecond)
	events := []monitor.AgentEvent{
		{
			Type:      monitor.EventSessionStarted,
			Timestamp: now,
			Data:      monitor.SessionStartedData{SessionID: "s1", Model: "claude-3"},
		},
		{
			Type:      monitor.EventTurnCompleted,
			Timestamp: now,
			Data:      monitor.TurnCompletedData{InputTokens: 1000, OutputTokens: 500, CostUSD: 0.05},
		},
		{
			Type:      monitor.EventToolCompleted,
			Timestamp: now,
			Data:      monitor.ToolCompletedData{ToolName: "bash", CallID: "c1", Success: true, DurationMs: 100},
		},
		{
			Type:      monitor.EventAgentMessage,
			Timestamp: now,
			Data:      monitor.AgentMessageData{Content: "Hello world"},
		},
	}

	for _, evt := range events {
		if err := s.Append(evt); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// Read back and verify typed Data payloads
	readEvents, err := s.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(readEvents) != 4 {
		t.Fatalf("expected 4 events, got %d", len(readEvents))
	}

	// SessionStartedData
	if d, ok := readEvents[0].Data.(monitor.SessionStartedData); !ok {
		t.Errorf("event 0: expected SessionStartedData, got %T", readEvents[0].Data)
	} else if d.SessionID != "s1" || d.Model != "claude-3" {
		t.Errorf("event 0: unexpected data: %+v", d)
	}

	// TurnCompletedData
	if d, ok := readEvents[1].Data.(monitor.TurnCompletedData); !ok {
		t.Errorf("event 1: expected TurnCompletedData, got %T", readEvents[1].Data)
	} else if d.InputTokens != 1000 || d.OutputTokens != 500 {
		t.Errorf("event 1: unexpected data: %+v", d)
	}

	// ToolCompletedData
	if d, ok := readEvents[2].Data.(monitor.ToolCompletedData); !ok {
		t.Errorf("event 2: expected ToolCompletedData, got %T", readEvents[2].Data)
	} else if d.ToolName != "bash" || !d.Success {
		t.Errorf("event 2: unexpected data: %+v", d)
	}

	// AgentMessageData
	if d, ok := readEvents[3].Data.(monitor.AgentMessageData); !ok {
		t.Errorf("event 3: expected AgentMessageData, got %T", readEvents[3].Data)
	} else if d.Content != "Hello world" {
		t.Errorf("event 3: unexpected content: %s", d.Content)
	}
}

func TestStore_Writer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	writer := s.Writer()
	if writer == nil {
		t.Fatal("Writer returned nil")
	}

	err = writer(monitor.AgentEvent{
		Type:      monitor.EventSessionStarted,
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("Writer: %v", err)
	}

	events, err := s.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}
