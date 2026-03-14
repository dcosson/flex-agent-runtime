package api

import "context"

// Terminal streaming types for the StreamTerminal RPC endpoint.
// These define the bidirectional protocol for raw terminal I/O between
// browser clients (via WebSocket relay) and the runtime's terminal sessions.

// StreamTerminalRequest is the initial attach request from a client.
type StreamTerminalRequest struct {
	SessionID string
}

// TerminalClientMessage is a message from the client to the terminal server.
type TerminalClientMessage struct {
	Input  *TerminalInput  `json:"input,omitempty"`
	Resize *TerminalResize `json:"resize,omitempty"`
}

// TerminalServerMessage is a message from the terminal server to the client.
type TerminalServerMessage struct {
	Attached *TerminalAttached `json:"attached,omitempty"`
	Output   *TerminalOutput   `json:"output,omitempty"`
	Detached *TerminalDetached `json:"detached,omitempty"`
}

// TerminalAttached is sent after successful subscription, including
// current dimensions and scrollback history for replay.
type TerminalAttached struct {
	Rows       int    `json:"rows"`
	Cols       int    `json:"cols"`
	Scrollback []byte `json:"scrollback,omitempty"`
}

// TerminalInput carries raw keystrokes from the client to the PTY.
type TerminalInput struct {
	Data []byte `json:"data"`
}

// TerminalOutput carries raw PTY output (including ANSI sequences) to the client.
type TerminalOutput struct {
	Data []byte `json:"data"`
}

// TerminalResize carries terminal dimension changes from the client.
type TerminalResize struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// TerminalDetached is sent when the session ends or the client is kicked.
type TerminalDetached struct {
	Reason string `json:"reason"` // "session_ended", "kicked", "error"
}

// MaxTerminalChunkSize is the maximum size of a single TerminalOutput message.
// Larger PTY reads are split into multiple messages.
const MaxTerminalChunkSize = 64 * 1024

// TerminalStreamHandle is the client-facing handle for a terminal stream.
// Callers use Send for input/resize and Recv for output/attached/detached.
type TerminalStreamHandle interface {
	Send(*TerminalClientMessage) error
	Recv() (*TerminalServerMessage, error)
	Close() error
}

// TerminalService handles bidirectional terminal streaming.
// It is a separate service from SandboxService — terminal streaming targets
// browser debug UIs, while SandboxService targets the agent loop.
type TerminalService interface {
	StreamTerminal(ctx context.Context, req *StreamTerminalRequest) (TerminalStreamHandle, error)
}
