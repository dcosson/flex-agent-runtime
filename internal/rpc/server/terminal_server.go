package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/rpc"
	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
	"github.com/anthropics/flex-agent-runtime/internal/termmux"
)

// TerminalServer implements api.TerminalService by bridging terminal
// subscriptions from termmux.SessionManager to the RPC layer.
// Each StreamTerminal call creates a subscription on the session,
// spawns bidirectional pump goroutines, and returns a stream handle.
type TerminalServer struct {
	sessionMgr *termmux.SessionManager
}

// NewTerminalServer creates a TerminalServer backed by the given session manager.
func NewTerminalServer(sessionMgr *termmux.SessionManager) *TerminalServer {
	return &TerminalServer{sessionMgr: sessionMgr}
}

// StreamTerminal opens a bidirectional terminal stream for the given session.
// The returned handle's first Recv yields the Attached message (with scrollback
// and dimensions), followed by live Output messages. The handle accepts Input
// and Resize messages via Send. When the session ends, a Detached message
// is delivered and the handle returns io.EOF.
func (s *TerminalServer) StreamTerminal(ctx context.Context, req *api.StreamTerminalRequest) (api.TerminalStreamHandle, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}

	session, err := s.sessionMgr.Get(req.SessionID)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.CodeNotFound, err.Error(), err)
	}

	subscriberID := generateSubscriberID()
	sub := session.SubscribeTerminal(subscriberID)

	ctx, cancel := context.WithCancel(ctx)

	toClient := make(chan *api.TerminalServerMessage, 64)
	fromClient := make(chan *api.TerminalClientMessage, 16)

	// Send initial attached message with scrollback and dimensions.
	toClient <- &api.TerminalServerMessage{
		Attached: &api.TerminalAttached{
			Rows:       sub.Rows,
			Cols:       sub.Cols,
			Scrollback: sub.Scrollback,
		},
	}

	// Output pump: subscription → toClient channel.
	go s.outputPump(ctx, cancel, sub, toClient, session, subscriberID)

	// Input pump: fromClient channel → session PTY.
	go s.inputPump(ctx, cancel, fromClient, session)

	return api.NewTerminalStreamHandle(toClient, fromClient, ctx, cancel), nil
}

// outputPump reads chunks from the subscription and forwards them as
// TerminalOutput messages. Closes toClient when done, which signals
// io.EOF to the handle's Recv.
func (s *TerminalServer) outputPump(
	ctx context.Context,
	cancel context.CancelFunc,
	sub *termmux.TerminalSubscription,
	toClient chan<- *api.TerminalServerMessage,
	session *termmux.Session,
	subscriberID string,
) {
	defer close(toClient)
	defer session.UnsubscribeTerminal(subscriberID)
	defer cancel()

	for {
		select {
		case chunk := <-sub.Chunks:
			// Split oversized chunks to respect MaxTerminalChunkSize.
			for len(chunk) > 0 {
				n := len(chunk)
				if n > api.MaxTerminalChunkSize {
					n = api.MaxTerminalChunkSize
				}
				select {
				case toClient <- &api.TerminalServerMessage{
					Output: &api.TerminalOutput{Data: chunk[:n]},
				}:
				case <-ctx.Done():
					return
				}
				chunk = chunk[n:]
			}
		case <-sub.Done:
			// Session ended — send detached and exit.
			select {
			case toClient <- &api.TerminalServerMessage{
				Detached: &api.TerminalDetached{Reason: "session_ended"},
			}:
			default:
			}
			return
		case <-ctx.Done():
			return
		}
	}
}

// inputPump reads client messages and processes input/resize.
// Input is rate-limited via a token bucket (10KB/s sustained, 64KB burst).
func (s *TerminalServer) inputPump(
	ctx context.Context,
	cancel context.CancelFunc,
	fromClient <-chan *api.TerminalClientMessage,
	session *termmux.Session,
) {
	defer cancel()

	// Token bucket rate limiter for input.
	const (
		maxInputRate   = 10 * 1024 // 10KB/s sustained
		inputBurst     = 64 * 1024 // 64KB burst (paste operations)
		refillInterval = 100 * time.Millisecond
		refillAmount   = maxInputRate / 10 // 1KB per 100ms tick
	)
	tokens := inputBurst
	ticker := time.NewTicker(refillInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-fromClient:
			if !ok {
				return
			}
			if msg == nil {
				continue
			}
			if msg.Input != nil && len(msg.Input.Data) > 0 {
				cost := len(msg.Input.Data)
				if tokens >= cost {
					tokens -= cost
					if _, err := session.WritePTY(msg.Input.Data); err != nil {
						slog.Warn("terminal input write failed",
							"session", session.ID, "error", err)
						return
					}
				} else {
					slog.Warn("terminal input rate limited, dropping",
						"session", session.ID, "bytes", cost, "tokens", tokens)
				}
			}
			if msg.Resize != nil && msg.Resize.Rows > 0 && msg.Resize.Cols > 0 {
				session.Resize(msg.Resize.Rows, msg.Resize.Cols)
			}
		case <-ticker.C:
			// Refill tokens.
			tokens += refillAmount
			if tokens > inputBurst {
				tokens = inputBurst
			}
		case <-ctx.Done():
			return
		}
	}
}

// generateSubscriberID creates a random subscriber identifier.
func generateSubscriberID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "term-" + hex.EncodeToString(b)
}

var _ api.TerminalService = (*TerminalServer)(nil)
