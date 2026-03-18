package fleet

import "errors"

// Sentinel errors returned by FleetSandboxControl. Error classification:
//
// | Error              | Retryable? | Description                                           |
// |--------------------|------------|-------------------------------------------------------|
// | ErrNoCapacity      | Yes        | No instance has available session slots; fleet may be  |
// |                    |            | scaling up. Caller should wait briefly and retry.      |
// | ErrFleetClosed     | No         | Fleet manager is shutting down. No new sandboxes.      |
// | parseSandboxID err | No         | SandboxID is malformed. Programming error.             |
// | Node RPC errors    | Depends    | Passed through from NodeSandboxControl.                |
// | Provisioner errors | Depends    | Cloud API errors may be transient or permanent.        |
var (
	// ErrNoCapacity indicates no instance has available session slots. The
	// fleet may be in the process of scaling up. Callers should wait briefly
	// (with backoff) and retry. The singleflight provision path handles
	// deduplication of concurrent scale-up requests automatically.
	ErrNoCapacity = errors.New("fleet: no capacity available")

	// ErrFleetClosed indicates the fleet manager is shutting down and cannot
	// accept new CreateSandbox calls. This error is permanent for the
	// lifetime of this FleetSandboxControl instance.
	ErrFleetClosed = errors.New("fleet: adapter is closed")

	// ErrInstanceNotFound indicates the instance ID parsed from a SandboxID
	// does not correspond to a known managed instance in the fleet.
	ErrInstanceNotFound = errors.New("fleet: instance not found")

	// ErrInvalidStateTransition indicates an attempted instance state
	// transition that is not permitted by the state machine.
	ErrInvalidStateTransition = errors.New("fleet: invalid state transition")

	// ErrInstanceDraining indicates an operation was attempted on an instance
	// that is in the draining state and not accepting new sessions.
	ErrInstanceDraining = errors.New("fleet: instance is draining")
)
