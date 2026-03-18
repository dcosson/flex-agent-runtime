package sandbox

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/gvisor"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/zfs"
)

func newLocalProcessService(t *testing.T) *SandboxHostService {
	t.Helper()
	cfg := DefaultServiceConfig()
	cfg.StorageBackend = StorageBackendLocalDisk
	cfg.ContainerRuntime = ContainerRuntimeNone
	cfg.SessionsRootDir = t.TempDir()
	cfg.AdvertiseAddr = "127.0.0.1"
	cfg.ShutdownTimeout = 200 * time.Millisecond
	cfg.PauseDrainTimeout = 200 * time.Millisecond
	svc, err := NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}
	return svc
}

func TestProcessLifecycleDirect(t *testing.T) {
	svc := newLocalProcessService(t)
	ctx := context.Background()

	if _, err := svc.CreateSession(ctx, CreateSessionRequest{SessionID: "proc-direct"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	launch, err := svc.LaunchProcess(ctx, LaunchProcessRequest{
		SessionID: "proc-direct",
		Binary:    "/bin/sh",
		Args:      []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("LaunchProcess: %v", err)
	}
	if launch.ProcessID == "" {
		t.Fatalf("expected process ID")
	}
	if launch.Status != ProcessStatusRunning {
		t.Fatalf("status = %q, want %q", launch.Status, ProcessStatusRunning)
	}

	status, err := svc.GetProcessStatus(ctx, GetProcessStatusRequest{SessionID: "proc-direct", ProcessID: launch.ProcessID})
	if err != nil {
		t.Fatalf("GetProcessStatus: %v", err)
	}
	if status.Status != ProcessStatusRunning {
		t.Fatalf("status = %q, want %q", status.Status, ProcessStatusRunning)
	}
	if status.ExitCode != nil {
		t.Fatalf("exit code should be nil while running")
	}

	if err := svc.KillProcess(ctx, KillProcessRequest{SessionID: "proc-direct", ProcessID: launch.ProcessID}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}
	// Kill is idempotent for already-exited processes.
	_ = svc.KillProcess(ctx, KillProcessRequest{SessionID: "proc-direct", ProcessID: launch.ProcessID})

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err = svc.GetProcessStatus(ctx, GetProcessStatusRequest{SessionID: "proc-direct", ProcessID: launch.ProcessID})
		if err != nil {
			t.Fatalf("GetProcessStatus after kill: %v", err)
		}
		if status.Status == ProcessStatusExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process did not exit in time; status=%q", status.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.ExitCode == nil {
		t.Fatalf("expected exit code after process exit")
	}
}

func TestPortProxyForwardsTraffic(t *testing.T) {
	svc := newLocalProcessService(t)

	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("target listen: %v", err)
	}
	t.Cleanup(func() { _ = target.Close() })
	targetPort := target.Addr().(*net.TCPAddr).Port

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := target.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		if _, readErr := io.ReadFull(conn, buf); readErr != nil {
			return
		}
		if string(buf) == "ping" {
			_, _ = conn.Write([]byte("pong"))
		}
	}()

	proxy, err := svc.createPortProxy(targetPort)
	if err != nil {
		t.Fatalf("createPortProxy: %v", err)
	}
	t.Cleanup(proxy.close)

	conn, err := net.DialTimeout("tcp", proxy.address, time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		_ = conn.Close()
		t.Fatalf("proxy write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		_ = conn.Close()
		t.Fatalf("proxy read: %v", err)
	}
	_ = conn.Close()
	if string(buf) != "pong" {
		t.Fatalf("proxy response = %q, want pong", string(buf))
	}
	<-done

	proxy.close()
	if _, err := net.DialTimeout("tcp", proxy.address, 100*time.Millisecond); err == nil {
		t.Fatalf("expected proxy listener to be closed")
	}
}

func TestDestroySessionKillsProcesses(t *testing.T) {
	svc := newLocalProcessService(t)
	ctx := context.Background()
	if _, err := svc.CreateSession(ctx, CreateSessionRequest{SessionID: "proc-destroy"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	launch, err := svc.LaunchProcess(ctx, LaunchProcessRequest{
		SessionID: "proc-destroy",
		Binary:    "/bin/sh",
		Args:      []string{"-c", "sleep 30"},
	})
	if err != nil {
		t.Fatalf("LaunchProcess: %v", err)
	}
	sess, err := svc.getSession("proc-destroy")
	if err != nil {
		t.Fatalf("getSession: %v", err)
	}
	sess.mu.RLock()
	proc := sess.processes[launch.ProcessID]
	sess.mu.RUnlock()
	if proc == nil {
		t.Fatalf("expected managed process entry")
	}

	if err := svc.DestroySession(ctx, "proc-destroy"); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	select {
	case <-proc.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("process monitor did not finish during destroy")
	}
	if _, err := svc.getSession("proc-destroy"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected session not found after destroy, got %v", err)
	}
}

func TestProcessLifecycleGVisor(t *testing.T) {
	zm := zfs.NewMockManager()
	gm := newMockGVisor()
	started := make(chan struct{}, 1)
	gm.runFn = func(ctx context.Context, _ gvisor.ContainerOptions) (*gvisor.ContainerResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	svc := newTestServiceWith(t, zm, gm)
	sess := createTestSession(t, svc, "proc-gvisor")

	launch, err := svc.LaunchProcess(context.Background(), LaunchProcessRequest{
		SessionID: sess.ID,
		Binary:    "flexagent",
		Args:      []string{"serve", "agent"},
	})
	if err != nil {
		t.Fatalf("LaunchProcess: %v", err)
	}
	if launch.Status != ProcessStatusStarting {
		t.Fatalf("status = %q, want %q", launch.Status, ProcessStatusStarting)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatalf("gvisor run was not invoked")
	}

	if err := svc.KillProcess(context.Background(), KillProcessRequest{
		SessionID: sess.ID,
		ProcessID: launch.ProcessID,
	}); err != nil {
		t.Fatalf("KillProcess: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := svc.GetProcessStatus(context.Background(), GetProcessStatusRequest{
			SessionID: sess.ID,
			ProcessID: launch.ProcessID,
		})
		if err != nil {
			t.Fatalf("GetProcessStatus: %v", err)
		}
		if status.Status == ProcessStatusExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("gvisor process did not exit in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
