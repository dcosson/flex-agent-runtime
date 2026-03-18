package instance

import (
	"context"
	"time"
)

// InstanceProvisioner manages cloud instance lifecycle. Implementations exist
// for each cloud provider (EC2, GCP, Azure). The fleet control loop calls
// these methods; it never touches cloud APIs directly.
type InstanceProvisioner interface {
	// LaunchInstance provisions a new instance from the given config.
	// Returns immediately with the instance ID and initial info. The instance
	// may still be booting (State == CloudInstancePending). The fleet manager
	// polls via DescribeInstance or HealthCheck to detect readiness.
	// This is non-blocking to avoid stalling the control loop.
	LaunchInstance(ctx context.Context, cfg InstanceConfig) (*InstanceInfo, error)

	// TerminateInstance permanently destroys an instance and all its resources.
	// Terminating an already-terminated instance is idempotent (no error).
	TerminateInstance(ctx context.Context, instanceID string) error

	// StopInstance stops an instance without terminating it. The instance can
	// be restarted with StartInstance. Storage is preserved.
	// Used for cost savings on warm-pool instances that have been idle too long.
	StopInstance(ctx context.Context, instanceID string) error

	// StartInstance restarts a previously stopped instance. Returns updated
	// InstanceInfo with potentially new IP addresses -- callers MUST use the
	// returned IPs rather than cached values. Cloud providers may reassign
	// IPs on stop/start (GCP releases external IP on stop; Azure deallocate
	// may change private IP; EC2 reassigns public IP if not Elastic). The
	// fleet control loop must update ManagedInstance.PrivateIP and recreate
	// the FleetNodeClient via SandboxClientFactory using the new address.
	StartInstance(ctx context.Context, instanceID string) (*InstanceInfo, error)

	// DescribeInstance returns current status of an instance from the cloud
	// provider's perspective (running, stopped, terminated, etc.).
	DescribeInstance(ctx context.Context, instanceID string) (*InstanceStatus, error)

	// ListInstances returns all instances matching the given filter. This is
	// essential for crash recovery: after a fleet manager restart, instance IDs
	// are lost and must be rediscovered from the cloud provider via tag-based
	// filtering (e.g., ManagedBy + fleet-id tags).
	ListInstances(ctx context.Context, filter InstanceFilter) ([]InstanceInfo, error)
}

// InstanceConfig specifies how to launch a new instance. Only provider-neutral
// fields are included here. Provider-specific launch configuration (security
// groups, subnets, key pairs, IAM roles, etc.) is passed to the provisioner
// constructor via typed provider config (e.g., EC2LaunchConfig), not through
// this interface.
type InstanceConfig struct {
	// Image is the pre-baked machine image ID (AMI for EC2, GCE image for GCP, etc.).
	Image string

	// InstanceType is the cloud-specific instance size (e.g., "m5.xlarge",
	// "n2-standard-4"). The provisioner interprets this for its provider.
	InstanceType string

	// UserData is cloud-init / startup script content (optional; should be
	// unnecessary with a properly baked image, but available for overrides).
	UserData string

	// Tags are metadata tags applied to the instance. Used for fleet discovery,
	// operational visibility, and crash recovery filtering.
	Tags map[string]string

	// DiskSizeGB is the root volume size in GB.
	DiskSizeGB int
}

// InstanceInfo is returned after launching or starting an instance.
type InstanceInfo struct {
	// InstanceID is the cloud provider's unique identifier for the instance.
	InstanceID string

	// PrivateIP is the instance's private (VPC-internal) IP address.
	PrivateIP string

	// PublicIP is the instance's public IP address. May be empty if no
	// public IP is assigned.
	PublicIP string

	// State is the current cloud-level state of the instance.
	State CloudInstanceState

	// LaunchTime is when the instance was launched. Zero value if unknown
	// (e.g., when returned from LaunchInstance before the cloud provider
	// assigns a timestamp).
	LaunchTime time.Time

	// Tags are the metadata tags on the instance.
	Tags map[string]string
}

// InstanceStatus is the cloud provider's view of an instance.
type InstanceStatus struct {
	// InstanceID is the cloud provider's unique identifier for the instance.
	InstanceID string

	// State is the current cloud-level state of the instance.
	State CloudInstanceState

	// StateReason is a human-readable explanation of the current state,
	// if provided by the cloud provider (e.g., "Client.UserInitiatedShutdown").
	// Empty if the provider does not supply a reason.
	StateReason string

	// PrivateIP is the instance's private IP address.
	PrivateIP string

	// PublicIP is the instance's public IP address.
	PublicIP string
}

// InstanceFilter specifies criteria for listing instances from the provider.
type InstanceFilter struct {
	// Tags filters instances that have all of these tags with matching values.
	// At minimum, used with {"ManagedBy": "flex-agent-runtime", "fleet-id": "<id>"}.
	Tags map[string]string

	// States filters to instances in these cloud states. If empty, all states
	// are returned (excluding terminated, which providers should omit by default).
	States []CloudInstanceState
}

// CloudInstanceState represents the instance state as reported by the cloud
// provider, normalized to a minimal provider-neutral set. Each provisioner
// implementation maps its native states to these values (e.g., EC2
// "shutting-down" -> CloudInstanceTerminating, GCP "STAGING" ->
// CloudInstancePending).
type CloudInstanceState string

const (
	// CloudInstancePending indicates the instance is launching / booting.
	CloudInstancePending CloudInstanceState = "pending"

	// CloudInstanceRunning indicates the instance is running and reachable.
	CloudInstanceRunning CloudInstanceState = "running"

	// CloudInstanceStopping indicates the instance is shutting down but not
	// yet terminated.
	CloudInstanceStopping CloudInstanceState = "stopping"

	// CloudInstanceStopped indicates the instance is stopped and can be
	// restarted.
	CloudInstanceStopped CloudInstanceState = "stopped"

	// CloudInstanceTerminating indicates the instance is being destroyed.
	CloudInstanceTerminating CloudInstanceState = "terminating"

	// CloudInstanceTerminated indicates the instance has been destroyed and
	// no longer exists.
	CloudInstanceTerminated CloudInstanceState = "terminated"
)

// AllCloudInstanceStates returns all defined CloudInstanceState values in a
// stable order. Useful for exhaustiveness checks in tests and state machine
// validation.
func AllCloudInstanceStates() []CloudInstanceState {
	return []CloudInstanceState{
		CloudInstancePending,
		CloudInstanceRunning,
		CloudInstanceStopping,
		CloudInstanceStopped,
		CloudInstanceTerminating,
		CloudInstanceTerminated,
	}
}
