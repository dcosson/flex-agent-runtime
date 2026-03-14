package codex

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"h2-agent-runtime/internal/termmux/driver"
	"h2-agent-runtime/internal/termmux/monitor"
)

const (
	driverName = "codex"
	cliCommand = "codex"
)

// Driver implements TermmuxDriverAdapter for the Codex CLI.
type Driver struct {
	mu           sync.Mutex
	eventHandler *EventHandler
	configDir    string
	events       chan<- monitor.AgentEvent
	stopped      bool
}

// Compile-time assertion.
var _ driver.TermmuxDriverAdapter = (*Driver)(nil)

// New creates a new Codex driver.
func New() *Driver {
	return &Driver{
		eventHandler: NewEventHandler(),
	}
}

func (d *Driver) Name() string    { return driverName }
func (d *Driver) Command() string { return cliCommand }

// BuildCommandArgs builds the CLI arguments for launching Codex.
func (d *Driver) BuildCommandArgs(prependArgs, extraArgs []string) []string {
	args := make([]string, 0, len(prependArgs)+len(extraArgs))
	args = append(args, prependArgs...)
	args = append(args, extraArgs...)
	return args
}

// BuildCommandEnvVars returns environment variables for the child process.
func (d *Driver) BuildCommandEnvVars(runtimeDir string) map[string]string {
	env := map[string]string{}
	// Codex config dir convention TBD — placeholder
	if d.configDir != "" {
		env["CODEX_CONFIG_DIR"] = d.configDir
	}
	return env
}

// PrepareForLaunch prepares the driver for launch.
func (d *Driver) PrepareForLaunch(dryRun bool) (driver.LaunchConfig, error) {
	if dryRun {
		return driver.LaunchConfig{}, nil
	}
	return driver.LaunchConfig{
		ExtraEnv: d.BuildCommandEnvVars(""),
	}, nil
}

// SetConfigDir sets the config directory for this driver instance.
func (d *Driver) SetConfigDir(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.configDir = dir
}

func (d *Driver) SupportsHooks() bool  { return false } // Not yet supported
func (d *Driver) SupportsResume() bool { return false } // TBD

// NativeSessionLogPath returns empty — Codex session log format is TBD.
func (d *Driver) NativeSessionLogPath(configDir, cwd, sessionID string) string {
	return "" // V1 non-goal
}

// ParseSessionLog is a V1 non-goal for Codex. Returns error.
func (d *Driver) ParseSessionLog(reader io.Reader) ([]driver.ConversationEntry, error) {
	// V1 non-goal: Codex session log conversion deferred until format investigation complete
	return nil, nil
}

// WriteSessionLog is a V1 non-goal for Codex. Returns error.
func (d *Driver) WriteSessionLog(entries []driver.ConversationEntry, writer io.Writer) error {
	// V1 non-goal
	return nil
}

// Start begins event processing.
func (d *Driver) Start(ctx context.Context, events chan<- monitor.AgentEvent) error {
	d.mu.Lock()
	d.events = events
	d.stopped = false
	d.mu.Unlock()

	// Set up idle callback to emit state change
	d.eventHandler.SetIdleCallback(func() {
		d.mu.Lock()
		ch := d.events
		d.mu.Unlock()
		if ch != nil {
			select {
			case ch <- monitor.AgentEvent{}: // Will trigger idle transition in monitor
			default:
			}
		}
	})

	return nil
}

// HandleHookEvent processes hook events. Codex hooks not yet supported.
func (d *Driver) HandleHookEvent(eventName string, payload json.RawMessage) bool {
	return false
}

// HandleInterrupt handles an interrupt signal with suppression window.
func (d *Driver) HandleInterrupt() bool {
	d.eventHandler.HandleInterrupt()
	return true
}

// Stop stops the driver.
func (d *Driver) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	d.eventHandler.Stop()
}

// OtelCallbacksForMonitor returns OTEL callbacks for Codex event normalization.
func (d *Driver) OtelCallbacksForMonitor(mon *monitor.AgentMonitor) (onLogs, onMetrics, onTraces func(json.RawMessage)) {
	return func(payload json.RawMessage) {
			for _, evt := range d.eventHandler.HandleOtelLogs(payload) {
				mon.Submit(evt)
			}
		},
		func(payload json.RawMessage) {
			// No-op for Codex metrics
		},
		func(payload json.RawMessage) {
			for _, evt := range d.eventHandler.HandleOtelTraces(payload) {
				mon.Submit(evt)
			}
		}
}

// EventHandler returns the underlying event handler for testing.
func (d *Driver) EventHandler() *EventHandler {
	return d.eventHandler
}
