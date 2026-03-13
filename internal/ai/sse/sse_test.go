package sse

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestScannerParsesEvents(t *testing.T) {
	input := "\ufeff:comment\n" +
		"event: message\n" +
		"data: hello\n" +
		"data: world\n" +
		"id: 42\n" +
		"\n" +
		"data: final\n\n"

	s := NewScanner(strings.NewReader(input))
	if !s.Next() {
		t.Fatalf("expected first event, err=%v", s.Err())
	}
	e := s.Event()
	if e.Type != "message" || e.Data != "hello\nworld" || e.ID != "42" {
		t.Fatalf("unexpected first event: %+v", e)
	}
	if !s.Next() {
		t.Fatalf("expected second event, err=%v", s.Err())
	}
	e = s.Event()
	if e.Data != "final" {
		t.Fatalf("unexpected second event: %+v", e)
	}
	if s.Next() {
		t.Fatalf("expected EOF")
	}
	if s.Err() != nil {
		t.Fatalf("unexpected err: %v", s.Err())
	}
}

// Sec2: resource limits.
func TestScannerResourceLimits(t *testing.T) {
	line := "data: " + strings.Repeat("x", 64) + "\n\n"
	s := NewScanner(strings.NewReader(line))
	s.SetLimits(16, 1024)
	if s.Next() {
		t.Fatalf("expected line-too-large failure")
	}
	if !errors.Is(s.Err(), ErrLineTooLarge) {
		t.Fatalf("expected ErrLineTooLarge, got %v", s.Err())
	}

	event := ""
	for i := 0; i < 20; i++ {
		event += "data: xxxxxxxxxx\n"
	}
	event += "\n"
	s = NewScanner(strings.NewReader(event))
	s.SetLimits(1024, 32)
	if s.Next() {
		t.Fatalf("expected event-too-large failure")
	}
	if !errors.Is(s.Err(), ErrEventTooLarge) {
		t.Fatalf("expected ErrEventTooLarge, got %v", s.Err())
	}
}

// F1: SSE parser fuzz seed coverage.
func FuzzScanner(f *testing.F) {
	f.Add("data: hello\\n\\n")
	f.Add(":comment\\n\\n")
	f.Add("event: x\\ndata: y\\n\\n")
	f.Add("data: a\\ndata: b\\n")
	f.Add("\ufeffdata: bom\\n\\n")

	f.Fuzz(func(t *testing.T, in string) {
		s := NewScanner(strings.NewReader(strings.ReplaceAll(in, "\\n", "\n")))
		for s.Next() {
			_ = s.Event()
		}
	})
}

// B2: SSE parsing benchmark.
func BenchmarkScanner100Events(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&buf, "event: m\ndata: chunk-%d\n\n", i)
	}
	input := buf.String()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := NewScanner(strings.NewReader(input))
		for s.Next() {
		}
		if err := s.Err(); err != nil {
			b.Fatalf("scanner err: %v", err)
		}
	}
}
