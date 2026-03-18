package agent

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

// AgentDriver is the runtime-level driver contract shared by native and adapter drivers.
type AgentDriver interface {
	Start(ctx context.Context, session *Session, prompt string) error
	Resume(ctx context.Context, session *Session) error
	Stop(ctx context.Context) error
	Subscribe(fn func(AgentEvent)) (unsubscribe func())
}

// DriverFactory creates an AgentDriver from config.
type DriverFactory func(cfg DriverConfig) (AgentDriver, error)

// DriverConfig carries optional dependencies into driver factories.
type DriverConfig struct {
	Model        ai.Model
	Options      ai.SimpleStreamOptions
	SystemPrompt string
	Tools        []AgentTool
	Metadata     map[string]any
}

var (
	driverRegistryMu sync.RWMutex
	driverRegistry   = map[string]DriverFactory{}
)

func RegisterDriver(name string, factory DriverFactory) error {
	if name == "" {
		return fmt.Errorf("driver name is required")
	}
	if factory == nil {
		return fmt.Errorf("driver factory is required")
	}

	driverRegistryMu.Lock()
	defer driverRegistryMu.Unlock()
	if _, exists := driverRegistry[name]; exists {
		return fmt.Errorf("driver already registered: %s", name)
	}
	driverRegistry[name] = factory
	return nil
}

func NewDriver(name string, cfg DriverConfig) (AgentDriver, error) {
	driverRegistryMu.RLock()
	factory, ok := driverRegistry[name]
	driverRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown driver: %s", name)
	}
	return factory(cfg)
}

func RegisteredDrivers() []string {
	driverRegistryMu.RLock()
	defer driverRegistryMu.RUnlock()
	out := make([]string, 0, len(driverRegistry))
	for name := range driverRegistry {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
