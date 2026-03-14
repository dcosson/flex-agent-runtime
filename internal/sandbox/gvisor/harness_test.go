package gvisor

import (
	"crypto/rand"
	"encoding/json"
	"math"
	"math/big"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Property-Based Tests (P1-P5)
// ============================================================================

// P1: Spec Generation Determinism
// BuildSpec(opts) must produce byte-identical JSON output for identical opts.
func TestP1_SpecDeterminism(t *testing.T) {
	for i := 0; i < 200; i++ {
		opts := randomContainerOptions()
		spec1, err1 := json.Marshal(BuildSpec(opts))
		spec2, err2 := json.Marshal(BuildSpec(opts))
		if err1 != nil || err2 != nil {
			t.Fatalf("marshal error: %v / %v", err1, err2)
		}
		if string(spec1) != string(spec2) {
			t.Fatalf("iteration %d: specs differ for identical options", i)
		}
	}
}

// P2: Resource Spec Round-Trip
// For any valid ResourceSpec, buildCgroupResources produces limits that match.
func TestP2_ResourceSpecRoundTrip(t *testing.T) {
	for i := 0; i < 200; i++ {
		cpus := randomFloat(0.1, 128.0)
		memMB := randomInt(1, 1024*1024)

		res := ResourceSpec{CPUs: cpus, MemoryMB: memMB}
		cgroup := buildCgroupResources(res)

		// CPU: quota/period should equal CPUs
		if cgroup.CPU != nil {
			actualCPUs := float64(*cgroup.CPU.Quota) / float64(*cgroup.CPU.Period)
			if math.Abs(cpus-actualCPUs) > 0.001 {
				t.Errorf("CPU round-trip: input=%f, got=%f", cpus, actualCPUs)
			}
		}

		// Memory: limit should equal memMB * 1024 * 1024
		if cgroup.Memory != nil {
			expectedMem := int64(memMB) * 1024 * 1024
			if *cgroup.Memory.Limit != expectedMem {
				t.Errorf("Memory round-trip: input=%d MB, got=%d bytes, want=%d",
					memMB, *cgroup.Memory.Limit, expectedMem)
			}
		}
	}
}

// P3: Container ID Uniqueness
// 10,000 consecutive calls produce no duplicates.
func TestP3_ContainerIDUniqueness(t *testing.T) {
	n := 10000
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := generateContainerID()
		if seen[id] {
			t.Fatalf("duplicate ID at iteration %d: %s", i, id)
		}
		seen[id] = true
	}
}

// P4: Validate Resources Accepts/Rejects Correctly
func TestP4_ValidateResourcesProperty(t *testing.T) {
	for i := 0; i < 500; i++ {
		res := ResourceSpec{
			CPUs:           randomFloat(-10, 300),
			MemoryMB:       randomInt(-1000, 2*1024*1024),
			MaxPIDs:        randomInt(-100, 100000),
			Timeout:        time.Duration(randomInt64(-1e9, 1e12)),
			MaxOutputBytes: randomInt64(-1000, 1e10),
		}

		err := ValidateResources(res)

		hasNegative := res.CPUs < 0 || res.MemoryMB < 0 || res.MaxPIDs < 0 ||
			res.Timeout < 0 || res.MaxOutputBytes < 0
		exceedsMax := res.CPUs > 256 || res.MemoryMB > 1024*1024

		if hasNegative || exceedsMax {
			if err == nil {
				t.Errorf("iteration %d: expected error for invalid spec %+v", i, res)
			}
		} else {
			if err != nil {
				t.Errorf("iteration %d: unexpected error for valid spec %+v: %v", i, res, err)
			}
		}
	}
}

// P5: LimitedBuffer Never Exceeds Max
func TestP5_LimitedBufferCapProperty(t *testing.T) {
	for i := 0; i < 200; i++ {
		maxBytes := int64(randomInt(1, 10*1024))
		buf := newLimitedBuffer(maxBytes)

		numWrites := randomInt(1, 100)
		var totalWritten int
		for j := 0; j < numWrites; j++ {
			writeSize := randomInt(0, 2048)
			data := make([]byte, writeSize)
			rand.Read(data)
			n, err := buf.Write(data)
			if err != nil {
				t.Fatalf("write error: %v", err)
			}
			if n != writeSize {
				t.Fatalf("write returned %d, want %d", n, writeSize)
			}
			totalWritten += writeSize
		}

		if int64(len(buf.Bytes())) > maxBytes {
			t.Errorf("buffer size %d exceeds max %d", len(buf.Bytes()), maxBytes)
		}
		if totalWritten > int(maxBytes) && !buf.Truncated() {
			t.Errorf("wrote %d bytes with max %d but Truncated()=false",
				totalWritten, maxBytes)
		}
	}
}

// ============================================================================
// Concurrency Tests (from F5 / S2)
// ============================================================================

// F5 (adapted): Concurrent Run + Close without panics
func TestF5_ConcurrentRunAndClose(t *testing.T) {
	m := newTestManagerWithMock(t, basicMockScript)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Run(t.Context(), validTestOpts())
		}()
	}

	time.Sleep(50 * time.Millisecond)
	m.Close()
	wg.Wait()

	if m.ActiveContainers() != 0 {
		t.Errorf("active containers = %d, want 0 after Close", m.ActiveContainers())
	}
}

// ============================================================================
// Benchmarks (B2, B5)
// ============================================================================

// B2: Spec Generation Throughput — target > 100,000 specs/sec
func BenchmarkBuildSpec(b *testing.B) {
	opts := ContainerOptions{
		Command:   []string{"bash", "-c", "go build ./..."},
		WorkDir:   "/workspace",
		RootFS:    "/pool/sessions/test",
		Resources: ResourceSpec{CPUs: 4, MemoryMB: 8192, MaxPIDs: 1024},
		Network:   NetworkNone,
		Env:       map[string]string{"GOPATH": "/go", "GOFLAGS": "-count=1"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spec := BuildSpec(opts)
		_ = spec
	}
}

// B5: LimitedBuffer Write Throughput — target > 1 GB/s
func BenchmarkLimitedBufferWrite(b *testing.B) {
	buf := newLimitedBuffer(10 * 1024 * 1024) // 10 MB limit
	data := make([]byte, 4096)
	rand.Read(data)

	b.ResetTimer()
	b.SetBytes(4096)
	for i := 0; i < b.N; i++ {
		buf.Write(data)
	}
}

func BenchmarkGenerateContainerID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = generateContainerID()
	}
}

func BenchmarkBuildCgroupResources(b *testing.B) {
	res := ResourceSpec{CPUs: 4, MemoryMB: 8192, MaxPIDs: 1024}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildCgroupResources(res)
	}
}

// ============================================================================
// Helpers for property tests (lightweight alternative to rapid)
// ============================================================================

func randomFloat(min, max float64) float64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return min + (max-min)*float64(n.Int64())/1000000.0
}

func randomInt(min, max int) int {
	if max <= min {
		return min
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	return min + int(n.Int64())
}

func randomInt64(min, max int64) int64 {
	if max <= min {
		return min
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(max-min))
	return min + n.Int64()
}

func randomContainerOptions() ContainerOptions {
	cpus := randomFloat(0, 8)
	memMB := randomInt(0, 4096)
	return ContainerOptions{
		Command:   []string{"/bin/echo", "test"},
		WorkDir:   "/workspace",
		RootFS:    "/rootfs/test",
		Resources: ResourceSpec{CPUs: cpus, MemoryMB: memMB, MaxPIDs: randomInt(0, 2048)},
		Network:   NetworkNone,
		Env: map[string]string{
			"A": "1",
			"B": "2",
		},
	}
}
