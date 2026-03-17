package native

import "github.com/anthropics/flex-agent-runtime/internal/sandbox"

type StorageBackend = sandbox.StorageBackend
type ContainerRuntime = sandbox.ContainerRuntime

const (
	StorageBackendZFS       = sandbox.StorageBackendZFS
	StorageBackendLocalDisk = sandbox.StorageBackendLocalDisk

	ContainerRuntimeGVisor = sandbox.ContainerRuntimeGVisor
	ContainerRuntimeNone   = sandbox.ContainerRuntimeNone
)

type NativeSandboxConfig struct {
	StorageBackend   StorageBackend
	ContainerRuntime ContainerRuntime
}

func DefaultConfig() NativeSandboxConfig {
	return NativeSandboxConfig{
		StorageBackend:   StorageBackendZFS,
		ContainerRuntime: ContainerRuntimeGVisor,
	}
}
