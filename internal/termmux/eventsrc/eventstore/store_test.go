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
