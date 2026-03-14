package termmux

// RPC message types for the StreamTerminal endpoint.
// These are Go struct definitions that will be used by the RPC layer (Plan 13)
// to implement the bidirectional terminal streaming protocol.

// StreamTerminalRequest is the initial attach request from the client.
type StreamTerminalRequest struct {
	SessionID string `json:"session_id"`
}

// TerminalClientMessage is a message from the client to the server.
type TerminalClientMessage struct {
	Attach *StreamTerminalRequest `json:"attach,omitempty"`
	Input  *TerminalInput         `json:"input,omitempty"`
	Resize *TerminalResize        `json:"resize,omitempty"`
}

// TerminalServerMessage is a message from the server to the client.
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

// MaxInputRateBytes is the maximum sustained input rate in bytes per second.
const MaxInputRateBytes = 10 * 1024

// InputBurstBytes is the burst allowance for input (paste operations).
const InputBurstBytes = 64 * 1024
