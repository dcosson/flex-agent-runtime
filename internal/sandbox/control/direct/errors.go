package direct

import "errors"

var (
	// ErrInstanceNotFound indicates the sandbox ID does not correspond to a
	// known managed instance.
	ErrInstanceNotFound = errors.New("direct: instance not found")

	// ErrProcessNotFound indicates the process ID does not correspond to a
	// known launched process on the given instance.
	ErrProcessNotFound = errors.New("direct: process not found")

	// ErrInstanceNotReady indicates the instance exists but is not in a state
	// where operations can be performed (e.g., stopped, pending).
	ErrInstanceNotReady = errors.New("direct: instance not ready")

	// ErrInstanceReadyTimeout indicates the instance did not reach "running"
	// state within Config.InstanceReadyTimeout.
	ErrInstanceReadyTimeout = errors.New("direct: instance ready timeout")

	// ErrProcessReadyTimeout indicates a launched process did not pass its
	// health check within Config.ProcessReadyTimeout.
	ErrProcessReadyTimeout = errors.New("direct: process ready timeout")

	// ErrSSMUnavailable indicates the SSM agent on the instance is not
	// responding or not registered.
	ErrSSMUnavailable = errors.New("direct: ssm agent unavailable")

	// ErrSSMTimeout indicates an SSM command did not complete within the
	// allowed time.
	ErrSSMTimeout = errors.New("direct: ssm command timeout")

	// ErrProcessLaunchFailed indicates the process launch command failed
	// (e.g., binary not found, port in use, permission denied).
	ErrProcessLaunchFailed = errors.New("direct: process launch failed")

	// ErrResourcesExceedMaximum indicates the requested resources exceed the
	// largest supported instance type mapping.
	ErrResourcesExceedMaximum = errors.New("direct: resources exceed maximum supported")

	// ErrDirectClosed indicates the adapter is shutting down and cannot
	// accept new CreateSandbox calls.
	ErrDirectClosed = errors.New("direct: adapter is closed")
)
