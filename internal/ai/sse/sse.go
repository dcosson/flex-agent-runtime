package sse

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	defaultMaxLineBytes  = 1 << 20 // 1 MiB
	defaultMaxEventBytes = 4 << 20 // 4 MiB
)

var (
	ErrLineTooLarge  = errors.New("sse: line too large")
	ErrEventTooLarge = errors.New("sse: event too large")
)

// Event is a parsed SSE event.
type Event struct {
	Type  string
	Data  string
	ID    string
	Retry string
}

// Scanner reads SSE events from an io.Reader.
type Scanner struct {
	r *bufio.Reader

	event Event
	err   error

	maxLineBytes  int
	maxEventBytes int
	bomStripped   bool
}

// NewScanner creates a scanner with default resource limits.
func NewScanner(r io.Reader) *Scanner {
	return &Scanner{
		r:             bufio.NewReader(r),
		maxLineBytes:  defaultMaxLineBytes,
		maxEventBytes: defaultMaxEventBytes,
	}
}

// SetLimits overrides per-line and per-event byte limits.
func (s *Scanner) SetLimits(maxLineBytes, maxEventBytes int) {
	if maxLineBytes > 0 {
		s.maxLineBytes = maxLineBytes
	}
	if maxEventBytes > 0 {
		s.maxEventBytes = maxEventBytes
	}
}

// Next parses the next SSE event.
func (s *Scanner) Next() bool {
	if s.err != nil {
		return false
	}

	var dataLines []string
	var eventType, id, retry string
	eventBytes := 0

	dispatch := func() bool {
		if len(dataLines) == 0 && eventType == "" && id == "" && retry == "" {
			return false
		}
		s.event = Event{
			Type:  eventType,
			Data:  strings.Join(dataLines, "\n"),
			ID:    id,
			Retry: retry,
		}
		return true
	}

	for {
		line, err := s.r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			s.err = err
			return false
		}
		if len(line) > s.maxLineBytes {
			s.err = fmt.Errorf("%w: %d > %d", ErrLineTooLarge, len(line), s.maxLineBytes)
			return false
		}

		if !s.bomStripped {
			line = strings.TrimPrefix(line, "\ufeff")
			s.bomStripped = true
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			if dispatch() {
				return true
			}
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			continue
		}

		if strings.HasPrefix(line, ":") {
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			continue
		}

		field := line
		value := ""
		if i := strings.IndexByte(line, ':'); i >= 0 {
			field = line[:i]
			value = line[i+1:]
			value = strings.TrimPrefix(value, " ")
		}

		eventBytes += len(field) + len(value)
		if eventBytes > s.maxEventBytes {
			s.err = fmt.Errorf("%w: %d > %d", ErrEventTooLarge, eventBytes, s.maxEventBytes)
			return false
		}

		switch field {
		case "event":
			eventType = value
		case "data":
			dataLines = append(dataLines, value)
		case "id":
			id = value
		case "retry":
			retry = value
		}

		if errors.Is(err, io.EOF) {
			if dispatch() {
				return true
			}
			return dispatch()
		}
	}
}

// Event returns the most recently parsed event.
func (s *Scanner) Event() Event { return s.event }

// Err returns the scanner terminal error.
func (s *Scanner) Err() error { return s.err }
