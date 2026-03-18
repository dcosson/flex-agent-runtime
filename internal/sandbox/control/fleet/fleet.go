package fleet

// FleetSandboxControl implements SandboxControl by managing a fleet of
// sandbox-host instances. It provisions instances via InstanceProvisioner,
// monitors health, maintains a warm pool, and routes sandbox operations
// to per-instance NodeSandboxControl clients.
//
// Lock ordering discipline (MUST be followed throughout):
//  1. FleetSandboxControl.mu  (fleet-level lock)
//  2. ManagedInstance.mu       (per-instance lock)
//
// The fleet lock is ALWAYS acquired before any instance lock. No code path
// may acquire FleetSandboxControl.mu while holding any ManagedInstance.mu.
// This prevents deadlocks between the control loop and concurrent callers.
//
// Fields will be added in bead aiag-1xd.3 (FleetSandboxControl Core).
type FleetSandboxControl struct{}
