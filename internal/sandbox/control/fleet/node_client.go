package fleet

import (
	"context"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

// FleetNodeClient combines SandboxControl with health checking. The fleet
// control loop needs both sandbox delegation and health polling from each
// instance. SandboxControl alone has no HealthCheck method, so this
// composite interface bridges the gap.
type FleetNodeClient interface {
	control.SandboxControl
	HealthCheck(ctx context.Context) (*HealthCheckResult, error)
}

// HealthCheckResult contains the health status reported by a sandbox-host
// instance. This is the fleet-local representation; the RPC layer converts
// to/from the wire format in internal/rpc/api.HealthCheckResponse.
type HealthCheckResult struct {
	Status       string
	SessionCount int
	ActiveTools  int
	Uptime       time.Duration
	Errors       []string
}

// SandboxClientFactory creates a FleetNodeClient for a given sandbox-host
// address. This is injected for testability — unit tests provide a mock
// factory, production code provides the real RPC client constructor
// wrapping the connection in a NodeSandboxControl + health client.
type SandboxClientFactory func(addr string) (FleetNodeClient, error)
