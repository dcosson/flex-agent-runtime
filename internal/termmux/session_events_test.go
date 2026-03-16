package termmux

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flex-agent-runtime/internal/termmux/eventsrc/otelserver"
)

func TestSessionEventSources_StartStop(t *testing.T) {
	s := NewSession("test-events", SessionConfig{
		Command: "/bin/echo",
		Args:    []string{"hello"},
	})

	sources, err := s.StartEventSources(EventSourceConfig{
		OtelCallbacks: otelserver.Callbacks{},
	})
	if err != nil {
		t.Fatalf("StartEventSources: %v", err)
	}

	endpoint := sources.OtelEndpoint()
	if endpoint == "" {
		t.Error("expected non-empty OTEL endpoint")
	}

	sources.Stop()
}

func TestSessionEventSources_WithSessionLog(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "session.jsonl")

	s := NewSession("test-events", SessionConfig{
		Command: "/bin/echo",
		Args:    []string{"hello"},
	})

	var lines []string
	var mu sync.Mutex

	sources, err := s.StartEventSources(EventSourceConfig{
		OtelCallbacks:  otelserver.Callbacks{},
		SessionLogPath: logPath,
		OnSessionLogLine: func(line []byte) {
			mu.Lock()
			lines = append(lines, string(line))
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("StartEventSources: %v", err)
	}
	defer sources.Stop()

	// Write a line to the log
	if err := os.WriteFile(logPath, []byte(`{"msg":"test"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Wait for tailer to pick it up
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(lines)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for session log line")
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	if lines[0] != `{"msg":"test"}` {
		t.Errorf("unexpected line: %s", lines[0])
	}
	mu.Unlock()
}

func TestSessionEventSources_OtelEndpointEmpty(t *testing.T) {
	ses := &SessionEventSources{}
	if ses.OtelEndpoint() != "" {
		t.Error("expected empty endpoint when no OTEL server")
	}
}

func TestSessionEventSources_FullIntegration(t *testing.T) {
	s := NewSession("test-integration", SessionConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hello; sleep 1"},
	})

	var otelReceived bool
	var mu sync.Mutex

	sources, err := s.StartEventSources(EventSourceConfig{
		OtelCallbacks: otelserver.Callbacks{
			OnLogs: func(body []byte) {
				mu.Lock()
				otelReceived = true
				mu.Unlock()
			},
		},
	})
	if err != nil {
		t.Fatalf("StartEventSources: %v", err)
	}
	defer sources.Stop()

	// Start the session with OTEL endpoint in env
	s.Config.Env = map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": sources.OtelEndpoint(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for session to exit
	select {
	case <-s.ExitNotify():
	case <-ctx.Done():
		t.Fatal("session did not exit within timeout")
	}

	// The OTEL callback may or may not have been called (echo doesn't send OTEL data)
	// but the integration should not panic or deadlock
	_ = otelReceived
}
