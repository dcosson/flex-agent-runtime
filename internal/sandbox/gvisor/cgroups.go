package gvisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ocispec "github.com/opencontainers/runtime-spec/specs-go"
)

// buildCgroupResources translates ResourceSpec into OCI cgroup config.
// Uses cgroup v2 unified hierarchy.
func buildCgroupResources(res ResourceSpec) *ocispec.LinuxResources {
	resources := &ocispec.LinuxResources{}

	// CPU limits
	if res.CPUs > 0 {
		period := uint64(100000) // 100ms standard period
		quota := int64(float64(period) * res.CPUs)
		resources.CPU = &ocispec.LinuxCPU{
			Quota:  &quota,
			Period: &period,
		}
	}

	// Memory limit
	if res.MemoryMB > 0 {
		limit := int64(res.MemoryMB) * 1024 * 1024
		resources.Memory = &ocispec.LinuxMemory{
			Limit: &limit,
			Swap:  &limit, // no swap beyond memory limit
		}
	}

	// PID limit
	maxPIDs := int64(res.MaxPIDs)
	if maxPIDs == 0 {
		maxPIDs = DefaultMaxPIDs
	}
	resources.Pids = &ocispec.LinuxPids{
		Limit: &maxPIDs,
	}

	return resources
}

// ValidateResources checks that resource limits are sensible.
func ValidateResources(res ResourceSpec) error {
	if res.CPUs < 0 {
		return fmt.Errorf("cpus must be non-negative, got %f", res.CPUs)
	}
	if res.CPUs > 256 {
		return fmt.Errorf("cpus %f exceeds maximum (256)", res.CPUs)
	}
	if res.MemoryMB < 0 {
		return fmt.Errorf("memory must be non-negative, got %d MB", res.MemoryMB)
	}
	if res.MemoryMB > 1024*1024 {
		return fmt.Errorf("memory %d MB exceeds maximum (1TB)", res.MemoryMB)
	}
	if res.MaxPIDs < 0 {
		return fmt.Errorf("max pids must be non-negative, got %d", res.MaxPIDs)
	}
	if res.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative, got %s", res.Timeout)
	}
	if res.MaxOutputBytes < 0 {
		return fmt.Errorf("max output bytes must be non-negative, got %d", res.MaxOutputBytes)
	}
	return nil
}

// detectOOMKill checks if the container was killed due to memory limit.
// Uses cgroup memory.events if accessible, falls back to exit code 137.
func detectOOMKill(cgroupPath string, exitCode int) bool {
	// Method 1: Read cgroup memory events
	if cgroupPath != "" {
		eventsPath := filepath.Join(cgroupPath, "memory.events")
		data, err := os.ReadFile(eventsPath)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "oom_kill ") {
					count := strings.TrimPrefix(line, "oom_kill ")
					if n, err := strconv.Atoi(strings.TrimSpace(count)); err == nil && n > 0 {
						return true
					}
				}
			}
		}
	}

	// Method 2: Exit code 137 = SIGKILL (128 + 9), common OOM indicator
	// Per P1 review finding: only when Status==StatusExited && ExitCode==137
	return exitCode == 137
}

// CgroupV2Entries returns the cgroup v2 filesystem entries for the given
// resource spec. This is used for testing and documentation — the actual
// cgroup setup is done by runsc via the OCI spec.
type CgroupV2Entry struct {
	File  string // e.g., "cpu.max"
	Value string // e.g., "200000 100000"
}

// ToCgroupV2Entries converts a ResourceSpec to cgroup v2 file entries.
func ToCgroupV2Entries(res ResourceSpec) []CgroupV2Entry {
	var entries []CgroupV2Entry

	// cpu.max: "$quota $period"
	if res.CPUs > 0 {
		period := 100000
		quota := int(float64(period) * res.CPUs)
		entries = append(entries, CgroupV2Entry{
			File:  "cpu.max",
			Value: fmt.Sprintf("%d %d", quota, period),
		})
	}

	// memory.max
	if res.MemoryMB > 0 {
		limit := int64(res.MemoryMB) * 1024 * 1024
		entries = append(entries, CgroupV2Entry{
			File:  "memory.max",
			Value: fmt.Sprintf("%d", limit),
		})
		// memory.swap.max = same as memory.max (disable swap)
		entries = append(entries, CgroupV2Entry{
			File:  "memory.swap.max",
			Value: fmt.Sprintf("%d", limit),
		})
	}

	// pids.max
	maxPIDs := res.MaxPIDs
	if maxPIDs == 0 {
		maxPIDs = DefaultMaxPIDs
	}
	entries = append(entries, CgroupV2Entry{
		File:  "pids.max",
		Value: fmt.Sprintf("%d", maxPIDs),
	})

	return entries
}
