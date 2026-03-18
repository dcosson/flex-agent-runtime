// Package direct implements DirectSandboxControl, which manages raw EC2
// instances as agent execution environments. Multiple agents share one
// instance without isolation. This is the "D2: EC2 Lightweight Sandbox"
// shape from the cloud-sandbox-providers shaping doc.
package direct

import (
	"log/slog"
	"sync"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// Default timeout values.
const (
	defaultInstanceReadyTimeout = 3 * time.Minute
	defaultProcessReadyTimeout  = 30 * time.Second
)

// Config configures the DirectSandboxControl adapter.
type Config struct {
	// AMIID is the AMI to launch instances from. Should have flexagent pre-installed.
	AMIID string

	// DefaultInstanceType is the EC2 instance type used when Resources is
	// zero-valued (e.g., "t3.medium", "m5.xlarge").
	DefaultInstanceType string

	// SubnetID is the VPC subnet for instance placement.
	SubnetID string

	// SecurityGroupIDs are the security groups to attach to instances.
	SecurityGroupIDs []string

	// InstanceProfileARN is the IAM instance profile for SSM access and
	// any AWS API calls from the instance.
	InstanceProfileARN string

	// KeyPairName is the SSH key pair for SSH fallback (optional; SSM is primary).
	KeyPairName string

	// RootVolumeSizeGB is the EBS root volume size in GiB (default 50).
	RootVolumeSizeGB int

	// RootVolumeType is the EBS volume type (default "gp3").
	RootVolumeType string

	// UserDataTemplate is a Go text/template script executed on instance boot.
	// Used to configure workspace directory, install dependencies, etc.
	UserDataTemplate string

	// Tags are applied to all resources created by this adapter. The adapter
	// always adds {"ManagedBy": "flex-agent-runtime", "adapter": "direct"}
	// in addition to any user-provided tags.
	Tags map[string]string

	// IPSelectionMode controls which IP address is used for connectivity.
	// Valid values: "public" (default), "private", "auto" (public if
	// available, else private).
	IPSelectionMode string

	// TerminateOnClose controls whether Close() terminates all managed
	// instances. When false (default), instances are left running for
	// potential recovery after restart.
	TerminateOnClose bool

	// InstanceReadyTimeout is the maximum time to wait for an instance to
	// reach "running" state and SSM registration (default 3 minutes).
	InstanceReadyTimeout time.Duration

	// ProcessReadyTimeout is the maximum time to wait for a launched process
	// to pass its health check (default 30 seconds).
	ProcessReadyTimeout time.Duration
}

// instanceStatus is a typed string for instance lifecycle states within the adapter.
type instanceStatus string

const (
	statusProvisioning instanceStatus = "provisioning"
	statusRunning      instanceStatus = "running"
	statusStopped      instanceStatus = "stopped"
)

// instanceState tracks the internal state of a managed EC2 instance.
type instanceState struct {
	instanceID string
	publicIP   string
	privateIP  string
	status     instanceStatus
	processes  map[string]*processState // processID -> state
}

// processState tracks the internal state of a launched process on an instance.
type processState struct {
	processID string
	pid       int
	startTime int64 // /proc/PID start time, for PID recycling detection
	binary    string
	args      []string
	port      int
	status    control.ProcessStatus
}

// UserDataTemplateData provides typed fields for UserData template rendering.
type UserDataTemplateData struct {
	// SandboxID is a placeholder ("direct:pending"). The actual sandbox ID
	// depends on the EC2 instance ID and is not known until after launch.
	// Use EC2 instance metadata if the startup script needs self-identification.
	SandboxID    string
	Labels       map[string]string // labels from the CreateSandbox request
	WorkspaceDir string            // workspace directory path (default "/workspace")
}

// DirectSandboxControl manages raw EC2 instances as agent environments.
// Multiple agents share one instance without isolation.
//
// Shutdown safety: The closing flag and inflightWg WaitGroup protect
// against orphaned instances during Close(). CreateSandbox checks closing
// before starting work. Close() sets closing, then waits on inflightWg
// for all in-flight CreateSandbox operations to complete before cleanup.
type DirectSandboxControl struct {
	provisioner instance.InstanceProvisioner
	ssmClient   SSMAPI
	logger      *slog.Logger
	config      Config

	mu         sync.Mutex
	instances  map[string]*instanceState // sandboxID -> state
	closing    bool                      // set under mu in Close(); checked in CreateSandbox
	inflightWg sync.WaitGroup            // tracks in-flight CreateSandbox operations
}

// Option configures a DirectSandboxControl.
type Option func(*DirectSandboxControl)

// WithLogger sets the structured logger for the adapter.
func WithLogger(logger *slog.Logger) Option {
	return func(d *DirectSandboxControl) {
		if logger != nil {
			d.logger = logger
		}
	}
}

// NewDirectSandboxControl creates a DirectSandboxControl with the given
// provisioner, SSM client, and configuration. The constructor is a stub
// for now -- crash recovery (Recover) will be added in a later task.
func NewDirectSandboxControl(provisioner instance.InstanceProvisioner, ssmClient SSMAPI, cfg Config, opts ...Option) *DirectSandboxControl {
	// Apply defaults for optional config fields.
	if cfg.InstanceReadyTimeout == 0 {
		cfg.InstanceReadyTimeout = defaultInstanceReadyTimeout
	}
	if cfg.ProcessReadyTimeout == 0 {
		cfg.ProcessReadyTimeout = defaultProcessReadyTimeout
	}
	if cfg.RootVolumeType == "" {
		cfg.RootVolumeType = "gp3"
	}
	if cfg.IPSelectionMode == "" {
		cfg.IPSelectionMode = "public"
	}

	d := &DirectSandboxControl{
		provisioner: provisioner,
		ssmClient:   ssmClient,
		logger:      slog.Default(),
		config:      cfg,
		instances:   make(map[string]*instanceState),
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// Capabilities returns the sandbox provider capabilities for the Direct
// adapter. Direct does not support snapshots or rollback (no ZFS). Pause
// is supported via EC2 stop/start, but is lossy -- all processes are
// killed and only EBS volumes survive.
func (d *DirectSandboxControl) Capabilities() control.SandboxCapabilities {
	return control.SandboxCapabilities{
		Snapshots:     false, // No ZFS, no instant snapshots
		Rollback:      false, // No rollback capability
		Pause:         true,  // EC2 stop/start (LOSSY -- processes die, EBS preserved)
		LaunchProcess: true,  // SSM-based process launch
		DeepPause:     false, // ZFS-to-S3 cold storage; not applicable to Direct
	}
}
