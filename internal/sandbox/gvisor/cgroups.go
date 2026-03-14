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

// detectOOMFromCgroup checks if the container was OOM-killed by reading
// cgroup v2 memory.events. Returns false if the cgroup path is unavailable
// or no OOM kill was recorded. Does NOT fall back to exit code heuristics;
// the caller is responsible for any exit-code-based classification.
func detectOOMFromCgroup(cgroupPath string) bool {
	if cgroupPath == "" {
		return false
	}

	eventsPath := filepath.Join(cgroupPath, "memory.events")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return false
	}

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "oom_kill ") {
			count := strings.TrimPrefix(line, "oom_kill ")
			if n, err := strconv.Atoi(strings.TrimSpace(count)); err == nil && n > 0 {
				return true
			}
		}
	}
	return false
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
		// memory.swap.max = 0 to disable swap (cgroup v2: controls swap-only portion)
		entries = append(entries, CgroupV2Entry{
			File:  "memory.swap.max",
			Value: "0",
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
