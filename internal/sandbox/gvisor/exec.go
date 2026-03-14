package gvisor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// containerState tracks a running container for lifecycle management.
type containerState struct {
	id        string
	startTime time.Time
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}
}

// runContainer invokes runsc and captures output.
func (m *Manager) runContainer(
	ctx context.Context,
	containerID string,
	bundlePath string,
	opts ContainerOptions,
) (*ContainerResult, error) {
	args := m.buildRunscArgs(opts, bundlePath, containerID)
	cmd := exec.CommandContext(ctx, m.config.RunscPath, args...)
	// WaitDelay ensures cmd.Wait returns promptly after the process is killed
	// even if child processes (e.g., from bash scripts) keep I/O pipes open.
	cmd.WaitDelay = 3 * time.Second

	// Set up output capture with size limits
	maxOutput := opts.Resources.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutput
	}
	stdoutCapture := newLimitedBuffer(maxOutput)
	stderrCapture := newLimitedBuffer(maxOutput)

	cmd.Stdout = stdoutCapture
	cmd.Stderr = stderrCapture

	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	// Track active container
	execCtx, execCancel := context.WithCancel(ctx)
	state := &containerState{
		id:        containerID,
		startTime: time.Now(),
		cmd:       cmd,
		cancel:    execCancel,
		done:      make(chan struct{}),
	}
	m.active.Store(containerID, state)
	m.activeCount.Add(1)
	defer func() {
		m.active.Delete(containerID)
		m.activeCount.Add(-1)
		close(state.done)
		execCancel()
	}()

	// Start the container
	bootStart := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runsc start: %w", err)
	}

	// Wait for completion
	waitErr := cmd.Wait()
	bootDuration := time.Since(bootStart)

	// Build result
	result := &ContainerResult{
		BootDuration:    bootDuration,
		Stdout:          stdoutCapture.Bytes(),
		Stderr:          stderrCapture.Bytes(),
		StdoutTruncated: stdoutCapture.Truncated(),
		StderrTruncated: stderrCapture.Truncated(),
	}

	// Determine exit status
	switch {
	case waitErr == nil:
		result.ExitCode = 0
		result.Status = StatusExited
	case execCtx.Err() == context.DeadlineExceeded:
		result.ExitCode = -1
		result.Status = StatusTimedOut
	case execCtx.Err() == context.Canceled:
		result.ExitCode = -1
		result.Status = StatusKilled
	default:
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Status = StatusExited
		} else {
			result.ExitCode = -1
			result.Status = StatusError
			return result, fmt.Errorf("runsc wait: %w", waitErr)
		}
	}

	// Check for OOM kill only for SIGKILL exits when status is still "exited".
	// Per implementation guide §1.12: only set OOMKilled=true when
	// Status==StatusExited && ExitCode==137.
	if result.Status == StatusExited && result.ExitCode == 137 {
		result.OOMKilled = m.checkOOMKill(containerID)
		if result.OOMKilled {
			result.Status = StatusOOMKilled
		}
	}

	return result, nil
}

// buildRunscArgs constructs the runsc command-line arguments.
func (m *Manager) buildRunscArgs(opts ContainerOptions, bundlePath, containerID string) []string {
	args := []string{
		"--root", m.config.RunscRoot,
		"--platform", m.config.Platform,
	}

	network := opts.Network
	if network == "" {
		network = m.config.DefaultNetwork
	}
	if network == "" {
		network = NetworkNone
	}
	args = append(args, "--network", string(network))

	args = append(args,
		"run",
		"--bundle", bundlePath,
		containerID,
	)
	return args
}

// deleteContainer removes a container via runsc delete. Best-effort.
func (m *Manager) deleteContainer(containerID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, m.config.RunscPath,
		"--root", m.config.RunscRoot,
		"delete", "--force", containerID,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		m.logger.Warn("container delete failed",
			"container_id", containerID,
			"error", err,
			"output", string(out),
		)
	}
}

// checkOOMKill checks if a container was OOM-killed using cgroup v2
// memory.events. Only called when exit code is 137 (SIGKILL).
// Returns true if cgroup data confirms an OOM kill, false otherwise
// (including when cgroup path is unavailable — exit code 137 alone
// is not sufficient to classify as OOM).
func (m *Manager) checkOOMKill(containerID string) bool {
	cgroupPath := m.cgroupPathForContainer(containerID)
	return detectOOMFromCgroup(cgroupPath)
}

// cgroupPathForContainer returns the cgroup v2 path for a container.
// Uses the standard runsc cgroup hierarchy under the configured root.
func (m *Manager) cgroupPathForContainer(containerID string) string {
	// runsc places container cgroups under /sys/fs/cgroup/<runsc-root>/<container-id>
	return filepath.Join("/sys/fs/cgroup", m.config.RunscRoot, containerID)
}

// generateContainerID creates a unique, runsc-compatible container ID.
// Format: "gv-<timestamp>-<random>" for time-ordered listing.
func generateContainerID() string {
	ts := time.Now().UnixMilli()
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("gv-%d-%s", ts, hex.EncodeToString(b))
}

// verifyRunsc checks that the runsc binary exists and is executable.
func verifyRunsc(path string) error {
	cmd := exec.Command(path, "--version")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("runsc not found or not executable at %q: %w", path, err)
	}

	version := strings.TrimSpace(string(out))
	if !strings.Contains(version, "runsc") {
		return fmt.Errorf("unexpected runsc version output: %q", version)
	}

	return nil
}

// limitedBuffer captures output up to a maximum size.
// Write returns len(p), nil even when truncation occurs to preserve
// compatibility with streaming copy loops. Callers must inspect
// Truncated() to detect data loss.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int64
	truncated atomic.Bool
	mu        sync.Mutex
}

func newLimitedBuffer(maxBytes int64) *limitedBuffer {
	return &limitedBuffer{max: maxBytes}
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	remaining := lb.max - int64(lb.buf.Len())
	if remaining <= 0 {
		lb.truncated.Store(true)
		return len(p), nil // pretend we wrote it all
	}
	if int64(len(p)) > remaining {
		lb.buf.Write(p[:remaining])
		lb.truncated.Store(true)
		return len(p), nil
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) Bytes() []byte {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return lb.buf.Bytes()
}

func (lb *limitedBuffer) Truncated() bool {
	return lb.truncated.Load()
}
