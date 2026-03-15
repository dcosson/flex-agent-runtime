package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/sandbox/zfs"
)

func TestNewSandboxHostService_BackendCombinations(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServiceConfig
		z       zfs.ZFSManager
		g       gvisor.GVisorManager
		wantErr bool
	}{
		{
			name: "zfs+gvisor",
			cfg: func() ServiceConfig {
				c := DefaultServiceConfig()
				c.StorageBackend = StorageBackendZFS
				c.ContainerRuntime = ContainerRuntimeGVisor
				c.PoolName = "pool"
				c.SessionsDataset = "pool/sessions"
				c.BasesDataset = "pool/bases"
				return c
			}(),
			z: zfs.NewMockManager(),
			g: newMockGVisor(),
		},
		{
			name: "zfs+none",
			cfg: func() ServiceConfig {
				c := DefaultServiceConfig()
				c.StorageBackend = StorageBackendZFS
				c.ContainerRuntime = ContainerRuntimeNone
				c.PoolName = "pool"
				c.SessionsDataset = "pool/sessions"
				c.BasesDataset = "pool/bases"
				return c
			}(),
			z: zfs.NewMockManager(),
		},
		{
			name: "localdisk+gvisor",
			cfg: func() ServiceConfig {
				c := DefaultServiceConfig()
				c.StorageBackend = StorageBackendLocalDisk
				c.ContainerRuntime = ContainerRuntimeGVisor
				c.SessionsRootDir = t.TempDir()
				return c
			}(),
			g: newMockGVisor(),
		},
		{
			name: "localdisk+none",
			cfg: func() ServiceConfig {
				c := DefaultServiceConfig()
				c.StorageBackend = StorageBackendLocalDisk
				c.ContainerRuntime = ContainerRuntimeNone
				c.SessionsRootDir = t.TempDir()
				return c
			}(),
		},
		{
			name: "invalid zfs without manager",
			cfg: func() ServiceConfig {
				c := DefaultServiceConfig()
				c.StorageBackend = StorageBackendZFS
				c.ContainerRuntime = ContainerRuntimeNone
				return c
			}(),
			wantErr: true,
		},
		{
			name: "invalid gvisor without manager",
			cfg: func() ServiceConfig {
				c := DefaultServiceConfig()
				c.StorageBackend = StorageBackendLocalDisk
				c.ContainerRuntime = ContainerRuntimeGVisor
				c.SessionsRootDir = t.TempDir()
				return c
			}(),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewSandboxHostService(tc.cfg, tc.z, tc.g, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected constructor error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewSandboxHostService error: %v", err)
			}
			if svc == nil {
				t.Fatal("service is nil")
			}
		})
	}
}

func TestCreateSessionLocalDisk_PathSafetyAndDestroy(t *testing.T) {
	cfg := DefaultServiceConfig()
	cfg.StorageBackend = StorageBackendLocalDisk
	cfg.ContainerRuntime = ContainerRuntimeNone
	cfg.SessionsRootDir = t.TempDir()
	cfg.ToolTimeout = 0

	svc, err := NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}

	_, err = svc.CreateSession(context.Background(), CreateSessionRequest{SessionID: "../escape"})
	if err == nil {
		t.Fatal("expected path-safety rejection")
	}

	info, err := svc.CreateSession(context.Background(), CreateSessionRequest{SessionID: "sess-safe"})
	if err != nil {
		t.Fatalf("CreateSession safe id: %v", err)
	}
	if !strings.HasPrefix(info.Mountpoint, filepath.Clean(cfg.SessionsRootDir)) {
		t.Fatalf("mountpoint %q outside root %q", info.Mountpoint, cfg.SessionsRootDir)
	}
	if _, statErr := os.Stat(info.Mountpoint); statErr != nil {
		t.Fatalf("session directory missing: %v", statErr)
	}
	if err := svc.DestroySession(context.Background(), "sess-safe"); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}
	if _, statErr := os.Stat(info.Mountpoint); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("session directory should be removed, stat err=%v", statErr)
	}
}

func TestExecuteTool_RuntimeNoneDowngradesTier2(t *testing.T) {
	cfg := DefaultServiceConfig()
	cfg.StorageBackend = StorageBackendLocalDisk
	cfg.ContainerRuntime = ContainerRuntimeNone
	cfg.SessionsRootDir = t.TempDir()
	cfg.ToolTimeout = 0
	svc, err := NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}

	if _, err := svc.CreateSession(context.Background(), CreateSessionRequest{SessionID: "sess-tier"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	resp, err := svc.ExecuteTool(context.Background(), ExecuteToolRequest{
		SessionID: "sess-tier",
		ToolName:  "bash",
		Params:    map[string]any{"cmd": "echo hi"},
	})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if resp.Tier != 1 {
		t.Fatalf("tier = %d, want 1 when runtime is none", resp.Tier)
	}
}

func TestSnapshotOps_NonZFSUnavailable(t *testing.T) {
	cfg := DefaultServiceConfig()
	cfg.StorageBackend = StorageBackendLocalDisk
	cfg.ContainerRuntime = ContainerRuntimeNone
	cfg.SessionsRootDir = t.TempDir()
	cfg.ToolTimeout = 0
	svc, err := NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}
	if _, err := svc.CreateSession(context.Background(), CreateSessionRequest{SessionID: "sess-snap"}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := svc.CreateSnapshot(context.Background(), "sess-snap", "snap-1"); !errors.Is(err, ErrSnapshotsNotAvailable) {
		t.Fatalf("CreateSnapshot err=%v, want ErrSnapshotsNotAvailable", err)
	}
	if err := svc.RollbackSession(context.Background(), "sess-snap", "snap-1"); !errors.Is(err, ErrSnapshotsNotAvailable) {
		t.Fatalf("RollbackSession err=%v, want ErrSnapshotsNotAvailable", err)
	}
}

func TestCapabilities_DynamicByConfig(t *testing.T) {
	cfg := DefaultServiceConfig()
	cfg.StorageBackend = StorageBackendLocalDisk
	cfg.ContainerRuntime = ContainerRuntimeNone
	cfg.SessionsRootDir = t.TempDir()
	svc, err := NewSandboxHostService(cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewSandboxHostService: %v", err)
	}
	caps := svc.Capabilities()
	if caps.Snapshots || caps.Rollback || caps.TierRouting {
		t.Fatalf("unexpected capabilities for local-disk/none: %+v", caps)
	}
	if !caps.Pause || !caps.StreamingProgress {
		t.Fatalf("expected pause/streaming true, got %+v", caps)
	}
}
