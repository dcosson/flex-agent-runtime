package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc"
)

// Orchestrator implements agentapi.AgentService and proxies sessions to
// placement-specific backends.
type Orchestrator struct {
	mu       sync.RWMutex
	sessions map[string]*sessionEntry
	closing  bool

	config OrchestratorConfig
	logger *slog.Logger

	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	wg sync.WaitGroup

	healthCancel context.CancelFunc
	healthWg     sync.WaitGroup
	healthClient *http.Client
}

var _ agentapi.AgentService = (*Orchestrator)(nil)

func New(cfg OrchestratorConfig) (*Orchestrator, error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	lifecycleCtx, lifecycleCancel := context.WithCancel(context.Background())
	orch := &Orchestrator{
		sessions:        make(map[string]*sessionEntry),
		config:          normalized,
		logger:          normalized.Logger,
		lifecycleCtx:    lifecycleCtx,
		lifecycleCancel: lifecycleCancel,
		healthCancel:    func() {},
		healthClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
	if normalized.HealthInterval > 0 {
		healthCtx, healthCancel := context.WithCancel(lifecycleCtx)
		orch.healthCancel = healthCancel
		orch.healthWg.Add(1)
		go func() {
			defer orch.healthWg.Done()
			orch.runHealthLoop(healthCtx)
		}()
	}
	return orch, nil
}

func (o *Orchestrator) beginOperation() error {
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		return rpc.NewRPCError(rpc.CodeUnavailable, "orchestrator is shutting down", nil)
	}
	o.wg.Add(1)
	o.mu.Unlock()
	return nil
}

func (o *Orchestrator) endOperation() {
	o.wg.Done()
}

func (o *Orchestrator) validatePlacement(placement PlacementMode) error {
	switch placement {
	case PlacementAgentDirect:
		if o.config.DirectControl == nil {
			return rpc.NewRPCError(rpc.CodeUnavailable, "placement mode agent-direct is not configured", nil)
		}
		return nil
	case PlacementAgentSandbox:
		if o.config.NodeControl == nil {
			return rpc.NewRPCError(rpc.CodeUnavailable, "placement mode agent-sandbox is not configured", nil)
		}
		return nil
	case PlacementToolsSandbox:
		if o.config.AgentLoopService == nil || o.config.NodeControl == nil {
			return rpc.NewRPCError(rpc.CodeUnavailable, "placement mode tools-sandbox is not configured", nil)
		}
		return nil
	default:
		return rpc.NewRPCError(rpc.CodeInvalidArgument, fmt.Sprintf("unsupported placement mode %q", placement), nil)
	}
}

func (o *Orchestrator) createTimeoutContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(o.lifecycleCtx, o.config.CreateTimeout)
}

func (o *Orchestrator) Close() error {
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		return nil
	}
	o.closing = true
	o.mu.Unlock()

	o.lifecycleCancel()
	o.healthCancel()
	o.healthWg.Wait()
	o.wg.Wait()

	entries := o.listSessionEntries()
	cleanupErrs := make([]error, 0)
	for _, entry := range entries {
		ctx, cancel := context.WithTimeout(context.Background(), o.config.ShutdownTimeout)
		_, err := o.destroySessionEntry(ctx, entry)
		cancel()
		if err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}

	if o.config.AgentLoopService != nil {
		if err := o.config.AgentLoopService.Close(); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}

	if dc, ok := o.config.DirectControl.(interface{ Close() error }); ok {
		if err := dc.Close(); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	if nc, ok := o.config.NodeControl.(interface{ Close() error }); ok {
		if err := nc.Close(); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}
