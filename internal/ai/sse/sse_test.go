package sse

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type alwaysErrReader struct{}

func (alwaysErrReader) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }

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

func TestScannerParsesRetryAndNoColonField(t *testing.T) {
	input := "event\n" +
		"retry: 2500\n" +
		"data: payload\n\n"

	s := NewScanner(strings.NewReader(input))
	if !s.Next() {
		t.Fatalf("expected event, err=%v", s.Err())
	}
	e := s.Event()
	if e.Type != "" {
		t.Fatalf("unexpected event type: %q", e.Type)
	}
	if e.Retry != "2500" {
		t.Fatalf("unexpected retry: %q", e.Retry)
	}
	if e.Data != "payload" {
		t.Fatalf("unexpected data: %q", e.Data)
	}
}

func TestScannerEOFDispatchesMetadataOnlyEvent(t *testing.T) {
	s := NewScanner(strings.NewReader("event: keep\nid: 7"))
	if !s.Next() {
		t.Fatalf("expected metadata-only event at EOF, err=%v", s.Err())
	}
	e := s.Event()
	if e.Type != "keep" || e.ID != "7" || e.Data != "" {
		t.Fatalf("unexpected event: %+v", e)
	}
	if s.Next() {
		t.Fatalf("expected EOF")
	}
	if s.Err() != nil {
		t.Fatalf("unexpected scanner err: %v", s.Err())
	}
}

func TestScannerPropagatesReadErrors(t *testing.T) {
	s := NewScanner(alwaysErrReader{})
	if s.Next() {
		t.Fatalf("expected Next=false on read error")
	}
	if !errors.Is(s.Err(), io.ErrUnexpectedEOF) {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", s.Err())
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
	input := buf.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewScanner(bytes.NewReader(input))
		for s.Next() {
			_ = s.Event()
		}
		if err := s.Err(); err != nil {
			b.Fatalf("scanner err: %v", err)
		}
	}
}

// B2-unsafe: SSE parsing benchmark using UnsafeEvent for zero-copy data.
func BenchmarkScanner100EventsUnsafe(b *testing.B) {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&buf, "event: m\ndata: chunk-%d\n\n", i)
	}
	input := buf.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewScanner(bytes.NewReader(input))
		for s.Next() {
			_ = s.UnsafeEvent()
		}
		if err := s.Err(); err != nil {
			b.Fatalf("scanner err: %v", err)
		}
	}
}

// B2-realistic: SSE parsing with Anthropic-style content_block_delta JSON.
func BenchmarkScanner100EventsRealistic(b *testing.B) {
	input := buildRealisticSSEInput()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewScanner(bytes.NewReader(input))
		for s.Next() {
			_ = s.Event()
		}
		if err := s.Err(); err != nil {
			b.Fatalf("scanner err: %v", err)
		}
	}
}

// B2-realistic-unsafe: Realistic SSE parsing with UnsafeEvent zero-copy path.
func BenchmarkScanner100EventsRealisticUnsafe(b *testing.B) {
	input := buildRealisticSSEInput()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := NewScanner(bytes.NewReader(input))
		for s.Next() {
			_ = s.UnsafeEvent()
		}
		if err := s.Err(); err != nil {
			b.Fatalf("scanner err: %v", err)
		}
	}
}

func buildRealisticSSEInput() []byte {
	var buf bytes.Buffer
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&buf, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"word%d \"}}\n\n", i)
	}
	return buf.Bytes()
}
