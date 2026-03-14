package gvisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Manager implements GVisorManager.
type Manager struct {
	config      ManagerConfig
	active      sync.Map // containerID → *containerState
	activeCount atomic.Int32
	sem         chan struct{} // concurrency limiter (nil if unlimited)
	logger      *slog.Logger
	closed      atomic.Bool
}

// NewManager creates a new GVisorManager.
// It validates the configuration and verifies that runsc is available.
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.RunscPath == "" {
		config.RunscPath = "runsc"
	}
	if config.BundleBaseDir == "" {
		config.BundleBaseDir = filepath.Join(os.TempDir(), "gvisor-bundles")
	}
	if config.RunscRoot == "" {
		if os.Getuid() == 0 {
			config.RunscRoot = "/run/runsc"
		} else {
			config.RunscRoot = filepath.Join(os.TempDir(), "runsc")
		}
	}
	if config.Platform == "" {
		config.Platform = "systrap"
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Verify runsc is available
	if err := verifyRunsc(config.RunscPath); err != nil {
		return nil, fmt.Errorf("runsc verification failed: %w", err)
	}

	// Create bundle base directory
	if err := os.MkdirAll(config.BundleBaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("create bundle dir: %w", err)
	}

	m := &Manager{
		config: config,
		logger: config.Logger.With("component", "gvisor-manager"),
	}

	if config.MaxConcurrentContainers > 0 {
		m.sem = make(chan struct{}, config.MaxConcurrentContainers)
	}

	return m, nil
}

// Run executes a command in a gVisor container with the given resource limits.
// Container is created, command runs, output is captured, and container is
// destroyed — all within this call.
func (m *Manager) Run(ctx context.Context, opts ContainerOptions) (*ContainerResult, error) {
	if m.closed.Load() {
		return nil, ErrManagerClosed
	}

	// Validate options
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid options: %w", err)
	}

	// Apply defaults from config
	opts = m.applyDefaults(opts)

	// Apply timeout from ResourceSpec if set
	if opts.Resources.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Resources.Timeout)
		defer cancel()
	}

	// Acquire concurrency slot
	if m.sem != nil {
		select {
		case m.sem <- struct{}{}:
			defer func() { <-m.sem }()
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for concurrency slot: %w", ctx.Err())
		}
	}

	// Re-check closed after potentially waiting for semaphore
	if m.closed.Load() {
		return nil, ErrManagerClosed
	}

	// Generate container ID
	containerID := generateContainerID()
	logger := m.logger.With("container_id", containerID)
	logger.InfoContext(ctx, "starting container",
		"command", opts.Command,
		"rootfs", opts.RootFS,
		"cpus", opts.Resources.CPUs,
		"memory_mb", opts.Resources.MemoryMB,
	)

	runStart := time.Now()

	// Phase 1: Build OCI spec
	spec := BuildSpec(opts)

	// Phase 2: Create bundle
	bundlePath, err := m.createBundle(containerID, spec)
	if err != nil {
		return nil, fmt.Errorf("create bundle: %w", err)
	}
	defer m.cleanupBundle(bundlePath)

	// Phase 3: Run container
	result, err := m.runContainer(ctx, containerID, bundlePath, opts)

	// Phase 4: Delete container (best-effort, always runs)
	m.deleteContainer(containerID)

	if err != nil {
		logger.ErrorContext(ctx, "container execution failed",
			"error", err,
			"duration", time.Since(runStart),
		)
		return nil, err
	}

	result.ContainerID = containerID
	result.Duration = time.Since(runStart)

	logger.InfoContext(ctx, "container completed",
		"exit_code", result.ExitCode,
		"status", result.Status,
		"duration", result.Duration,
		"boot_duration", result.BootDuration,
		"oom_killed", result.OOMKilled,
	)

	return result, nil
}

// ActiveContainers returns the number of currently running containers.
func (m *Manager) ActiveContainers() int {
	return int(m.activeCount.Load())
}

// Close shuts down the manager. Idempotent and safe for concurrent callers.
// Marks manager closed immediately; subsequent Run() returns ErrManagerClosed.
// Cancels in-flight runs, waits up to 5s grace for exit, then force-deletes.
func (m *Manager) Close() error {
	if !m.closed.CompareAndSwap(false, true) {
		return nil // already closed
	}

	m.logger.Info("shutting down gvisor manager")

	// Cancel all active containers and wait for them
	m.active.Range(func(key, value any) bool {
		state := value.(*containerState)
		state.cancel()
		// Wait briefly for container to exit
		select {
		case <-state.done:
		case <-time.After(5 * time.Second):
			m.logger.Warn("container did not exit during shutdown",
				"container_id", state.id,
			)
		}
		m.deleteContainer(state.id)
		return true
	})

	return nil
}

// applyDefaults merges default resource/network settings from ManagerConfig.
func (m *Manager) applyDefaults(opts ContainerOptions) ContainerOptions {
	if opts.Network == "" {
		opts.Network = m.config.DefaultNetwork
	}
	if opts.Resources.CPUs == 0 && m.config.DefaultResources.CPUs > 0 {
		opts.Resources.CPUs = m.config.DefaultResources.CPUs
	}
	if opts.Resources.MemoryMB == 0 && m.config.DefaultResources.MemoryMB > 0 {
		opts.Resources.MemoryMB = m.config.DefaultResources.MemoryMB
	}
	if opts.Resources.MaxPIDs == 0 && m.config.DefaultResources.MaxPIDs > 0 {
		opts.Resources.MaxPIDs = m.config.DefaultResources.MaxPIDs
	}
	if opts.Resources.Timeout == 0 && m.config.DefaultResources.Timeout > 0 {
		opts.Resources.Timeout = m.config.DefaultResources.Timeout
	}
	if opts.Resources.MaxOutputBytes == 0 && m.config.DefaultResources.MaxOutputBytes > 0 {
		opts.Resources.MaxOutputBytes = m.config.DefaultResources.MaxOutputBytes
	}
	return opts
}
