// Package eventstore provides JSONL event persistence.
package eventstore

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"h2-agent-runtime/internal/termmux/monitor"
)

// Store persists events to a JSONL file.
type Store struct {
	mu   sync.Mutex
	path string
	file *os.File
	enc  *json.Encoder
}

// New creates a new event store at the given path.
func New(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open event store: %w", err)
	}
	return &Store{
		path: path,
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

// Append writes an event to the store.
func (s *Store) Append(evt monitor.AgentEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return fmt.Errorf("event store is closed")
	}
	return s.enc.Encode(evt)
}

// ReadAll reads all events from the store file.
func (s *Store) ReadAll() ([]monitor.AgentEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("open event store for read: %w", err)
	}
	defer f.Close()

	var events []monitor.AgentEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)

	for scanner.Scan() {
		var evt monitor.AgentEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue // Skip malformed lines
		}
		events = append(events, evt)
	}

	return events, scanner.Err()
}

// Close closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	s.enc = nil
	return err
}

// Writer returns a function suitable for monitor.WithEventWriter.
func (s *Store) Writer() func(monitor.AgentEvent) error {
	return s.Append
}
