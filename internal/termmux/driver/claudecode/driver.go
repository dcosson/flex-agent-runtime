package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"flex-agent-runtime/internal/termmux/driver"
	"flex-agent-runtime/internal/termmux/monitor"
)

const (
	driverName = "claude_code"
	cliCommand = "claude"
)

// Driver implements TermmuxDriverAdapter for Claude Code CLI.
type Driver struct {
	mu           sync.Mutex
	eventHandler *EventHandler
	configDir    string
	events       chan<- monitor.AgentEvent
	stopped      bool
}

// Compile-time assertion.
var _ driver.TermmuxDriverAdapter = (*Driver)(nil)

// New creates a new Claude Code driver.
func New() *Driver {
	return &Driver{
		eventHandler: NewEventHandler(),
	}
}

func (d *Driver) Name() string    { return driverName }
func (d *Driver) Command() string { return cliCommand }

// BuildCommandArgs builds the CLI arguments for launching Claude Code.
func (d *Driver) BuildCommandArgs(prependArgs, extraArgs []string) []string {
	args := make([]string, 0, len(prependArgs)+len(extraArgs))
	args = append(args, prependArgs...)
	args = append(args, extraArgs...)
	return args
}

// BuildCommandEnvVars returns environment variables to set for the child process.
func (d *Driver) BuildCommandEnvVars(runtimeDir string) map[string]string {
	env := map[string]string{}
	if d.configDir != "" {
		env["CLAUDE_CONFIG_DIR"] = d.configDir
	}
	return env
}

// PrepareForLaunch prepares the driver for launch, including config directory setup.
func (d *Driver) PrepareForLaunch(dryRun bool) (driver.LaunchConfig, error) {
	if dryRun {
		return driver.LaunchConfig{}, nil
	}

	return driver.LaunchConfig{
		ExtraEnv: d.BuildCommandEnvVars(""),
	}, nil
}

// SetConfigDir sets the config directory for this driver instance.
// Called by the session manager before launch.
func (d *Driver) SetConfigDir(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.configDir = dir
}

func (d *Driver) SupportsHooks() bool  { return true }
func (d *Driver) SupportsResume() bool { return true }

// NativeSessionLogPath returns the path to Claude Code's native session.jsonl.
func (d *Driver) NativeSessionLogPath(configDir, cwd, sessionID string) string {
	if configDir != "" {
		return filepath.Join(configDir, "projects", cwd, sessionID, "session.jsonl")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects", cwd, sessionID, "session.jsonl")
}

// ParseSessionLog reads Claude Code's session.jsonl format.
func (d *Driver) ParseSessionLog(reader io.Reader) ([]driver.ConversationEntry, error) {
	return ParseSessionLog(reader)
}

// WriteSessionLog writes to Claude Code's session.jsonl format.
func (d *Driver) WriteSessionLog(entries []driver.ConversationEntry, writer io.Writer) error {
	return WriteSessionLog(entries, writer)
}

// Start begins event processing. The events channel receives normalized events.
func (d *Driver) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	d.mu.Lock()
	d.events = events
	d.stopped = false
	d.mu.Unlock()
	return nil
}

// HandleHookEvent processes a hook event from Claude Code.
func (d *Driver) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	d.mu.Lock()
	events := d.events
	stopped := d.stopped
	d.mu.Unlock()

	if stopped || events == nil {
		return false
	}

	evts := d.eventHandler.HandleHookEvent(eventName, payload)
	for _, evt := range evts {
		select {
		case events <- evt:
		default:
			// Drop if channel is full
		}
	}

	return len(evts) > 0
}

// HandleInterrupt handles an interrupt signal.
func (d *Driver) HandleInterrupt() bool {
	return false // No special interrupt handling for Claude Code
}

// Stop stops the driver.
func (d *Driver) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
}

// OtelCallbacksForMonitor returns OTEL callbacks that route through the event handler
// and submit to the monitor.
func (d *Driver) OtelCallbacksForMonitor(mon *monitor.AgentMonitor) (onLogs, onMetrics, onTraces func(json.RawMessage)) {
	return func(payload json.RawMessage) {
			for _, evt := range d.eventHandler.HandleOtelLogs(payload) {
				mon.Submit(evt)
			}
		},
		func(payload json.RawMessage) {
			for _, evt := range d.eventHandler.HandleOtelMetrics(payload) {
				mon.Submit(evt)
			}
		},
		func(payload json.RawMessage) {
			for _, evt := range d.eventHandler.HandleOtelTraces(payload) {
				mon.Submit(evt)
			}
		}
}

// SessionLogCallbackForMonitor returns a callback that processes session log lines
// and submits events to the monitor.
func (d *Driver) SessionLogCallbackForMonitor(mon *monitor.AgentMonitor) func([]byte) {
	return func(line []byte) {
		for _, evt := range d.eventHandler.HandleSessionLogLine(line) {
			mon.Submit(evt)
		}
	}
}

// EventHandler returns the underlying event handler for testing.
func (d *Driver) EventHandler() *EventHandler {
	return d.eventHandler
}

// DiscoveredSessionID returns the session ID discovered from OTEL events.
func (d *Driver) DiscoveredSessionID() string {
	return d.eventHandler.SessionID()
}

// Errors returns any accumulated errors from the event handler.
func (d *Driver) Errors() []error {
	return nil
}

// MakeOtelConfig creates an OTEL endpoint-aware config for the child process env.
func MakeOtelConfig(endpoint string) map[string]string {
	if endpoint == "" {
		return nil
	}
	return map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": endpoint,
	}
}

// FormatSessionLogPath returns the session log path for given parameters.
func FormatSessionLogPath(configDir, cwd, sessionID string) string {
	return fmt.Sprintf("%s/projects/%s/%s/session.jsonl", configDir, cwd, sessionID)
}
