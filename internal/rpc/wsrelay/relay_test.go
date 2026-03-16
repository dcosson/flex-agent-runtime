package wsrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flex-agent-runtime/internal/rpc/api"
	"flex-agent-runtime/internal/rpc/server"
	"flex-agent-runtime/internal/termmux"

	"github.com/coder/websocket"
)

func newRelayTestStack(t *testing.T) (*httptest.Server, *termmux.SessionManager) {
	t.Helper()
	sm := termmux.NewSessionManager()
	sess, err := sm.Create("test", termmux.SessionConfig{
		Command:     "cat",
		InitialRows: 24,
		InitialCols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := sess.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Stop() })

	termSrv := server.NewTerminalServer(sm)
	relay := NewRelay(termSrv, nil)
	ts := httptest.NewServer(relay.Handler())
	t.Cleanup(ts.Close)
	return ts, sm
}

func TestRelayBasicLifecycle(t *testing.T) {
	ts, _ := newRelayTestStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect via WebSocket
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "?session_id=test"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// First message should be Attached
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	var attached api.TerminalServerMessage
	if err := json.Unmarshal(data, &attached); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if attached.Attached == nil {
		t.Fatal("expected Attached message")
	}
	if attached.Attached.Rows != 24 || attached.Attached.Cols != 80 {
		t.Fatalf("wrong dimensions: %dx%d", attached.Attached.Rows, attached.Attached.Cols)
	}

	// Send input via WebSocket
	inputMsg := api.TerminalClientMessage{
		Input: &api.TerminalInput{Data: []byte("wstest\n")},
	}
	inputData, _ := json.Marshal(inputMsg)
	if err := conn.Write(ctx, websocket.MessageText, inputData); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Read output — should eventually contain "wstest"
	deadline := time.After(3 * time.Second)
	var collected []byte
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for echo, collected: %q", collected)
		default:
		}

		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("Read error: %v (collected: %q)", err, collected)
		}
		var msg api.TerminalServerMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("Unmarshal error: %v", err)
		}
		if msg.Output != nil {
			collected = append(collected, msg.Output.Data...)
			if strings.Contains(string(collected), "wstest") {
				return // success
			}
		}
	}
}

func TestRelayMissingSessionID(t *testing.T) {
	ts, _ := newRelayTestStack(t)

	// HTTP request without session_id should get 400
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRelayAuthRejection(t *testing.T) {
	sm := termmux.NewSessionManager()
	termSrv := server.NewTerminalServer(sm)
	relay := NewRelay(termSrv, func(r *http.Request) error {
		token := r.Header.Get("Authorization")
		if token != "Bearer valid-token" {
			return fmt.Errorf("unauthorized")
		}
		return nil
	})
	ts := httptest.NewServer(relay.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Connect without auth — should be rejected
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "?session_id=test"
	_, _, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Fatal("expected auth rejection")
	}
}

func TestRelayNonexistentSession(t *testing.T) {
	sm := termmux.NewSessionManager()
	termSrv := server.NewTerminalServer(sm)
	relay := NewRelay(termSrv, nil)
	ts := httptest.NewServer(relay.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "?session_id=nonexistent"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		// Connection rejected during upgrade — this is OK
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// If upgrade succeeded, the server should close the connection quickly
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error reading from nonexistent session")
	}
}

func TestRelayResize(t *testing.T) {
	ts, sm := newRelayTestStack(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "?session_id=test"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Consume attached
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Send resize
	resizeMsg := api.TerminalClientMessage{
		Resize: &api.TerminalResize{Rows: 50, Cols: 160},
	}
	data, _ := json.Marshal(resizeMsg)
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}

	// Wait for resize to propagate
	time.Sleep(100 * time.Millisecond)

	sess, _ := sm.Get("test")
	sess.VT.Mu.Lock()
	rows, cols := sess.VT.Rows, sess.VT.Cols
	sess.VT.Mu.Unlock()

	if rows != 50 || cols != 160 {
		t.Fatalf("resize not applied via WS relay: got %dx%d, want 50x160", rows, cols)
	}
}
