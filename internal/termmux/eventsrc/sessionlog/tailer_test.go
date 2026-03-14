package sessionlog

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTailer_BasicPolling(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	var lines []string
	var mu sync.Mutex

	tailer := New(logPath, func(line []byte) {
		mu.Lock()
		lines = append(lines, string(line))
		mu.Unlock()
	}, WithPollInterval(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = tailer.Start(ctx)
	}()

	// Write some lines
	time.Sleep(100 * time.Millisecond) // Let tailer start
	if err := os.WriteFile(logPath, []byte(`{"msg":"line1"}`+"\n"+`{"msg":"line2"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Wait for tailer to pick up
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(lines)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("timed out waiting for lines, got %d: %v", len(lines), lines)
			mu.Unlock()
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	if lines[0] != `{"msg":"line1"}` {
		t.Errorf("line 0: got %q", lines[0])
	}
	if lines[1] != `{"msg":"line2"}` {
		t.Errorf("line 1: got %q", lines[1])
	}
	mu.Unlock()
}

func TestTailer_FileAppearsLater(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	var lines []string
	var mu sync.Mutex

	tailer := New(logPath, func(line []byte) {
		mu.Lock()
		lines = append(lines, string(line))
		mu.Unlock()
	}, WithPollInterval(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = tailer.Start(ctx)
	}()

	// File doesn't exist yet, tailer should keep polling
	time.Sleep(200 * time.Millisecond)

	// Now create the file
	if err := os.WriteFile(logPath, []byte(`{"event":"delayed"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(lines)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for delayed line")
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	if lines[0] != `{"event":"delayed"}` {
		t.Errorf("got %q", lines[0])
	}
	mu.Unlock()
}

func TestTailer_AppendedLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	var lines []string
	var mu sync.Mutex

	tailer := New(logPath, func(line []byte) {
		mu.Lock()
		lines = append(lines, string(line))
		mu.Unlock()
	}, WithPollInterval(50*time.Millisecond))

	// Write initial content
	if err := os.WriteFile(logPath, []byte(`{"n":1}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = tailer.Start(ctx)
	}()

	// Wait for initial line
	waitForLines(t, &mu, &lines, 1, 2*time.Second)

	// Append more lines
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	_, _ = f.WriteString(`{"n":2}` + "\n")
	_, _ = f.WriteString(`{"n":3}` + "\n")
	f.Close()

	waitForLines(t, &mu, &lines, 3, 2*time.Second)
}

func TestTailer_DoubleStart(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	tailer := New(logPath, func(line []byte) {})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = tailer.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Second start should return error
	err := tailer.Start(ctx)
	if err == nil {
		t.Error("expected error on double start")
	}
}

func TestTailer_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	tailer := New(logPath, func(line []byte) {}, WithPollInterval(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- tailer.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancellation")
	}
}

func waitForLines(t *testing.T, mu *sync.Mutex, lines *[]string, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		mu.Lock()
		count := len(*lines)
		mu.Unlock()
		if count >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d lines, got %d", n, count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
