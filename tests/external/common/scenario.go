package common

import (
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/native"
)

// BackendConfig represents a named backend configuration for testing.
type BackendConfig struct {
	Name             string
	StorageBackend   native.StorageBackend
	ContainerRuntime native.ContainerRuntime
}

// StandardConfigs returns the four standard backend configurations (C1-C4).
func StandardConfigs() []BackendConfig {
	return []BackendConfig{
		{Name: "C1-full", StorageBackend: native.StorageBackendZFS, ContainerRuntime: native.ContainerRuntimeGVisor},
		{Name: "C2-zfs-only", StorageBackend: native.StorageBackendZFS, ContainerRuntime: native.ContainerRuntimeNone},
		{Name: "C3-gvisor-only", StorageBackend: native.StorageBackendLocalDisk, ContainerRuntime: native.ContainerRuntimeGVisor},
		{Name: "C4-minimal", StorageBackend: native.StorageBackendLocalDisk, ContainerRuntime: native.ContainerRuntimeNone},
	}
}

// SupportsSnapshots returns whether this config supports snapshots.
func (c BackendConfig) SupportsSnapshots() bool {
	return c.StorageBackend == native.StorageBackendZFS
}

// SupportsTierRouting returns whether this config supports tier routing.
func (c BackendConfig) SupportsTierRouting() bool {
	return c.ContainerRuntime == native.ContainerRuntimeGVisor
}
