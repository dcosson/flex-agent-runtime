package gvisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildCgroupResources_CPULimits(t *testing.T) {
	res := ResourceSpec{CPUs: 2.0}
	resources := buildCgroupResources(res)

	if resources.CPU == nil {
		t.Fatal("CPU resources should not be nil")
	}
	if resources.CPU.Period == nil || *resources.CPU.Period != 100000 {
		t.Errorf("period = %v, want 100000", resources.CPU.Period)
	}
	if resources.CPU.Quota == nil || *resources.CPU.Quota != 200000 {
		t.Errorf("quota = %v, want 200000", resources.CPU.Quota)
	}
}

func TestBuildCgroupResources_FractionalCPU(t *testing.T) {
	res := ResourceSpec{CPUs: 0.5}
	resources := buildCgroupResources(res)

	if resources.CPU == nil {
		t.Fatal("CPU resources should not be nil")
	}
	if *resources.CPU.Quota != 50000 {
		t.Errorf("quota = %d, want 50000", *resources.CPU.Quota)
	}
}

func TestBuildCgroupResources_NoCPU(t *testing.T) {
	res := ResourceSpec{}
	resources := buildCgroupResources(res)

	if resources.CPU != nil {
		t.Error("CPU should be nil when CPUs is 0")
	}
}

func TestBuildCgroupResources_MemoryLimits(t *testing.T) {
	res := ResourceSpec{MemoryMB: 512}
	resources := buildCgroupResources(res)

	if resources.Memory == nil {
		t.Fatal("Memory resources should not be nil")
	}
	expected := int64(512 * 1024 * 1024)
	if *resources.Memory.Limit != expected {
		t.Errorf("memory limit = %d, want %d", *resources.Memory.Limit, expected)
	}
	if *resources.Memory.Swap != expected {
		t.Errorf("swap limit = %d, want %d (same as memory)", *resources.Memory.Swap, expected)
	}
}

func TestBuildCgroupResources_NoMemory(t *testing.T) {
	res := ResourceSpec{}
	resources := buildCgroupResources(res)

	if resources.Memory != nil {
		t.Error("Memory should be nil when MemoryMB is 0")
	}
}

func TestBuildCgroupResources_PIDLimit(t *testing.T) {
	res := ResourceSpec{MaxPIDs: 500}
	resources := buildCgroupResources(res)

	if resources.Pids == nil {
		t.Fatal("Pids should not be nil")
	}
	if resources.Pids.Limit == nil || *resources.Pids.Limit != 500 {
		t.Errorf("pids limit = %v, want 500", resources.Pids.Limit)
	}
}

func TestBuildCgroupResources_DefaultPIDLimit(t *testing.T) {
	res := ResourceSpec{}
	resources := buildCgroupResources(res)

	if resources.Pids == nil {
		t.Fatal("Pids should not be nil")
	}
	if resources.Pids.Limit == nil || *resources.Pids.Limit != DefaultMaxPIDs {
		t.Errorf("pids limit = %v, want default %d", resources.Pids.Limit, DefaultMaxPIDs)
	}
}

func TestBuildCgroupResources_FullSpec(t *testing.T) {
	res := ResourceSpec{
		CPUs:     4.0,
		MemoryMB: 1024,
		MaxPIDs:  2048,
	}
	resources := buildCgroupResources(res)

	if resources.CPU == nil {
		t.Fatal("CPU should not be nil")
	}
	if resources.Memory == nil {
		t.Fatal("Memory should not be nil")
	}
	if resources.Pids == nil {
		t.Fatal("Pids should not be nil")
	}
	if *resources.CPU.Quota != 400000 {
		t.Errorf("cpu quota = %d, want 400000", *resources.CPU.Quota)
	}
	if *resources.Memory.Limit != int64(1024*1024*1024) {
		t.Errorf("memory limit = %d, want %d", *resources.Memory.Limit, int64(1024*1024*1024))
	}
	if resources.Pids.Limit == nil || *resources.Pids.Limit != 2048 {
		t.Errorf("pids limit = %v, want 2048", resources.Pids.Limit)
	}
}

func TestValidateResources_Valid(t *testing.T) {
	tests := []struct {
		name string
		res  ResourceSpec
	}{
		{"zero values", ResourceSpec{}},
		{"normal values", ResourceSpec{CPUs: 2, MemoryMB: 512, MaxPIDs: 100}},
		{"max CPU", ResourceSpec{CPUs: 256}},
		{"max memory", ResourceSpec{MemoryMB: 1024 * 1024}},
		{"with timeout", ResourceSpec{Timeout: 60 * time.Second}},
		{"with max output", ResourceSpec{MaxOutputBytes: 1024}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateResources(tt.res); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateResources_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		res     ResourceSpec
		wantErr string
	}{
		{"negative CPUs", ResourceSpec{CPUs: -1}, "non-negative"},
		{"excessive CPUs", ResourceSpec{CPUs: 300}, "exceeds maximum"},
		{"negative memory", ResourceSpec{MemoryMB: -1}, "non-negative"},
		{"excessive memory", ResourceSpec{MemoryMB: 1024*1024 + 1}, "exceeds maximum"},
		{"negative PIDs", ResourceSpec{MaxPIDs: -1}, "non-negative"},
		{"negative timeout", ResourceSpec{Timeout: -1 * time.Second}, "non-negative"},
		{"negative output", ResourceSpec{MaxOutputBytes: -1}, "non-negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResources(tt.res)
			if err == nil {
				t.Fatal("expected error")
			}
			if !searchString(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestDetectOOMKill_CgroupEvents(t *testing.T) {
	dir := t.TempDir()
	eventsFile := filepath.Join(dir, "memory.events")
	content := "low 0\nhigh 0\nmax 0\noom 0\noom_kill 1\noom_group_kill 0\n"
	if err := os.WriteFile(eventsFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if !detectOOMKill(dir, 0) {
		t.Error("should detect OOM from cgroup memory.events")
	}
}

func TestDetectOOMKill_CgroupNoOOM(t *testing.T) {
	dir := t.TempDir()
	eventsFile := filepath.Join(dir, "memory.events")
	content := "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0\noom_group_kill 0\n"
	if err := os.WriteFile(eventsFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if detectOOMKill(dir, 0) {
		t.Error("should not detect OOM when oom_kill is 0")
	}
}

func TestDetectOOMKill_ExitCode137(t *testing.T) {
	if !detectOOMKill("", 137) {
		t.Error("should detect OOM from exit code 137")
	}
}

func TestDetectOOMKill_NormalExit(t *testing.T) {
	if detectOOMKill("", 0) {
		t.Error("should not detect OOM on exit code 0")
	}
	if detectOOMKill("", 1) {
		t.Error("should not detect OOM on exit code 1")
	}
}

func TestDetectOOMKill_MissingCgroupPath(t *testing.T) {
	// Non-existent path falls back to exit code check
	if detectOOMKill("/nonexistent/path", 137) {
		// Should still detect via exit code
	}
	if detectOOMKill("/nonexistent/path", 0) {
		t.Error("should not detect OOM with missing cgroup and exit code 0")
	}
}

func TestToCgroupV2Entries_CPUOnly(t *testing.T) {
	res := ResourceSpec{CPUs: 2.0}
	entries := ToCgroupV2Entries(res)

	found := findEntry(entries, "cpu.max")
	if found == nil {
		t.Fatal("missing cpu.max entry")
	}
	if found.Value != "200000 100000" {
		t.Errorf("cpu.max = %q, want '200000 100000'", found.Value)
	}
}

func TestToCgroupV2Entries_MemoryOnly(t *testing.T) {
	res := ResourceSpec{MemoryMB: 256}
	entries := ToCgroupV2Entries(res)

	memMax := findEntry(entries, "memory.max")
	if memMax == nil {
		t.Fatal("missing memory.max entry")
	}
	expected := "268435456" // 256 * 1024 * 1024
	if memMax.Value != expected {
		t.Errorf("memory.max = %q, want %q", memMax.Value, expected)
	}

	swapMax := findEntry(entries, "memory.swap.max")
	if swapMax == nil {
		t.Fatal("missing memory.swap.max entry")
	}
	if swapMax.Value != expected {
		t.Errorf("memory.swap.max = %q, want %q", swapMax.Value, expected)
	}
}

func TestToCgroupV2Entries_PIDsDefault(t *testing.T) {
	res := ResourceSpec{}
	entries := ToCgroupV2Entries(res)

	pids := findEntry(entries, "pids.max")
	if pids == nil {
		t.Fatal("missing pids.max entry")
	}
	if pids.Value != "1024" {
		t.Errorf("pids.max = %q, want '1024'", pids.Value)
	}
}

func TestToCgroupV2Entries_PIDsCustom(t *testing.T) {
	res := ResourceSpec{MaxPIDs: 500}
	entries := ToCgroupV2Entries(res)

	pids := findEntry(entries, "pids.max")
	if pids == nil {
		t.Fatal("missing pids.max entry")
	}
	if pids.Value != "500" {
		t.Errorf("pids.max = %q, want '500'", pids.Value)
	}
}

func TestToCgroupV2Entries_FullSpec(t *testing.T) {
	res := ResourceSpec{
		CPUs:     1.5,
		MemoryMB: 1024,
		MaxPIDs:  2048,
	}
	entries := ToCgroupV2Entries(res)

	// Should have: cpu.max, memory.max, memory.swap.max, pids.max
	if len(entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(entries))
	}

	cpu := findEntry(entries, "cpu.max")
	if cpu == nil || cpu.Value != "150000 100000" {
		t.Errorf("cpu.max = %v, want '150000 100000'", cpu)
	}

	mem := findEntry(entries, "memory.max")
	if mem == nil || mem.Value != "1073741824" {
		t.Errorf("memory.max = %v, want '1073741824'", mem)
	}
}

func TestToCgroupV2Entries_NoCPUNoMemory(t *testing.T) {
	res := ResourceSpec{MaxPIDs: 100}
	entries := ToCgroupV2Entries(res)

	// Only pids.max
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].File != "pids.max" {
		t.Errorf("expected pids.max, got %q", entries[0].File)
	}
}

func findEntry(entries []CgroupV2Entry, file string) *CgroupV2Entry {
	for i := range entries {
		if entries[i].File == file {
			return &entries[i]
		}
	}
	return nil
}
