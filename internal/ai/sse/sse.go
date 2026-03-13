package sse

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"unsafe"
)

const (
	defaultMaxLineBytes  = 1 << 20 // 1 MiB
	defaultMaxEventBytes = 4 << 20 // 4 MiB
)

var (
	ErrLineTooLarge  = errors.New("sse: line too large")
	ErrEventTooLarge = errors.New("sse: event too large")

	bomPrefix = []byte("\ufeff")
)

// Event is a parsed SSE event.
type Event struct {
	Type  string
	Data  string
	ID    string
	Retry string
}

// Scanner reads SSE events from an io.Reader.
//
// §17.1 Zero-copy optimization: uses ReadSlice for zero-alloc line reads,
// accumulates data in a reusable byte buffer, and defers string conversion
// to Event()/UnsafeEvent() access time.
type Scanner struct {
	r *bufio.Reader

	event         Event // Type, ID, Retry set at dispatch; Data set lazily by Event()
	err           error
	dataConverted bool // true after Event() has populated event.Data from dataBuf

	maxLineBytes  int
	maxEventBytes int
	bomStripped   bool

	// Reusable buffers for zero-copy parsing (§17.1).
	dataBuf []byte // accumulated data field bytes, reused across events
	lineBuf []byte // for lines spanning the bufio internal buffer
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

// readLine reads a single line using ReadSlice for zero-alloc reads.
// Falls back to accumulating in lineBuf when lines span the bufio buffer.
func (s *Scanner) readLine() ([]byte, error) {
	line, err := s.r.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		return line, err
	}
	// Line spans the bufio buffer boundary — accumulate in lineBuf.
	s.lineBuf = append(s.lineBuf[:0], line...)
	for err == bufio.ErrBufferFull {
		line, err = s.r.ReadSlice('\n')
		s.lineBuf = append(s.lineBuf, line...)
	}
	return s.lineBuf, err
}

// Next parses the next SSE event.
func (s *Scanner) Next() bool {
	if s.err != nil {
		return false
	}

	s.dataBuf = s.dataBuf[:0]
	s.dataConverted = false
	dataCount := 0
	var eventType, id, retry string
	eventBytes := 0

	dispatch := func() bool {
		if dataCount == 0 && eventType == "" && id == "" && retry == "" {
			return false
		}
		// Data is NOT converted here — deferred to Event()/UnsafeEvent().
		s.event = Event{
			Type:  eventType,
			ID:    id,
			Retry: retry,
		}
		return true
	}

	for {
		line, err := s.readLine()
		if err != nil && !errors.Is(err, io.EOF) {
			s.err = err
			return false
		}
		if len(line) > s.maxLineBytes {
			s.err = fmt.Errorf("%w: %d > %d", ErrLineTooLarge, len(line), s.maxLineBytes)
			return false
		}

		if !s.bomStripped {
			line = bytes.TrimPrefix(line, bomPrefix)
			s.bomStripped = true
		}

		line = bytes.TrimRight(line, "\r\n")

		if len(line) == 0 {
			if dispatch() {
				return true
			}
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			continue
		}

		if line[0] == ':' {
			if errors.Is(err, io.EOF) {
				return dispatch()
			}
			continue
		}

		field := line
		var value []byte
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			field = line[:i]
			value = line[i+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		eventBytes += len(field) + len(value)
		if eventBytes > s.maxEventBytes {
			s.err = fmt.Errorf("%w: %d > %d", ErrEventTooLarge, eventBytes, s.maxEventBytes)
			return false
		}

		// switch string(byteSlice) with constant cases is a Go compiler
		// optimization that avoids allocation.
		switch string(field) {
		case "event":
			eventType = string(value)
		case "data":
			if dataCount > 0 {
				s.dataBuf = append(s.dataBuf, '\n')
			}
			s.dataBuf = append(s.dataBuf, value...)
			dataCount++
		case "id":
			id = string(value)
		case "retry":
			retry = string(value)
		}

		if errors.Is(err, io.EOF) {
			if dispatch() {
				return true
			}
			return dispatch()
		}
	}
}

// Event returns the most recently parsed event with safe string Data.
func (s *Scanner) Event() Event {
	if !s.dataConverted {
		s.event.Data = string(s.dataBuf)
		s.dataConverted = true
	}
	return s.event
}

// UnsafeEvent returns the event with zero-copy Data backed by internal buffers.
// The returned Event.Data is only valid until the next call to Next().
// Use for hot paths where the caller processes data immediately.
func (s *Scanner) UnsafeEvent() Event {
	e := s.event
	if len(s.dataBuf) > 0 {
		e.Data = unsafe.String(unsafe.SliceData(s.dataBuf), len(s.dataBuf))
	}
	return e
}

// Err returns the scanner terminal error.
func (s *Scanner) Err() error { return s.err }
