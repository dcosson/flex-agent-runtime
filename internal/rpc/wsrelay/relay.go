// Package wsrelay bridges WebSocket connections to the TerminalService RPC.
// It accepts WebSocket upgrades with a session_id query parameter, opens a
// StreamTerminal call, and pumps WebSocket frames ↔ RPC messages.
//
// All messages are JSON text frames. Terminal output bytes are base64-encoded
// within the JSON TerminalServerMessage/TerminalClientMessage structs.
package wsrelay

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"flex-agent-runtime/internal/rpc/api"

	"github.com/coder/websocket"
)

// AuthFunc validates an HTTP request before allowing the WebSocket upgrade.
// Return nil to allow, non-nil error to reject. The error message is sent
// as a 403 response body.
type AuthFunc func(r *http.Request) error

// Relay bridges WebSocket connections to a TerminalService.
type Relay struct {
	termSvc api.TerminalService
	auth    AuthFunc
	logger  *slog.Logger
}

// NewRelay creates a WebSocket relay backed by the given TerminalService.
// If auth is nil, no authentication is performed on the WebSocket upgrade.
func NewRelay(termSvc api.TerminalService, auth AuthFunc) *Relay {
	return &Relay{
		termSvc: termSvc,
		auth:    auth,
		logger:  slog.Default(),
	}
}

// Handler returns an http.Handler that accepts WebSocket connections.
// Expected URL: /terminal/stream?session_id=<id>
func (r *Relay) Handler() http.Handler {
	return http.HandlerFunc(r.handleHTTP)
}

func (r *Relay) handleHTTP(w http.ResponseWriter, req *http.Request) {
	// Auth check before WebSocket upgrade.
	if r.auth != nil {
		if err := r.auth(req); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
	}

	sessionID := req.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id query parameter is required", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, req, &websocket.AcceptOptions{
		// Allow all origins for dev; tighten in production.
		InsecureSkipVerify: true,
	})
	if err != nil {
		r.logger.Error("websocket accept failed", "error", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	r.handleConn(req.Context(), conn, sessionID)
}

func (r *Relay) handleConn(ctx context.Context, conn *websocket.Conn, sessionID string) {
	// Open terminal stream.
	handle, err := r.termSvc.StreamTerminal(ctx, &api.StreamTerminalRequest{SessionID: sessionID})
	if err != nil {
		r.logger.Error("StreamTerminal failed", "session", sessionID, "error", err)
		conn.Close(websocket.StatusInternalError, "session not found")
		return
	}
	defer handle.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	// Server→client pump: terminal output → WebSocket frames.
	go func() {
		for {
			msg, recvErr := handle.Recv()
			if recvErr != nil {
				errCh <- recvErr
				return
			}
			data, marshalErr := json.Marshal(msg)
			if marshalErr != nil {
				errCh <- marshalErr
				return
			}
			if writeErr := conn.Write(ctx, websocket.MessageText, data); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()

	// Client→server pump: WebSocket frames → terminal input.
	go func() {
		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			var msg api.TerminalClientMessage
			if unmarshalErr := json.Unmarshal(data, &msg); unmarshalErr != nil {
				r.logger.Warn("invalid client message", "error", unmarshalErr)
				continue // skip malformed messages
			}
			if sendErr := handle.Send(&msg); sendErr != nil {
				errCh <- sendErr
				return
			}
		}
	}()

	// Wait for either pump to finish.
	<-errCh
}
