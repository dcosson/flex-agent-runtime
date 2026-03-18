//go:build docker

package tier2

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/tests/external/common"
)

func sandboxHostURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SANDBOX_HOST_URL")
	if url == "" {
		t.Fatal("SANDBOX_HOST_URL not set")
	}
	return url
}

func activeConfig(t *testing.T) common.BackendConfig {
	t.Helper()
	sb, cr := common.SandboxHostConfig(t)
	for _, cfg := range common.StandardConfigs() {
		if string(cfg.StorageBackend) == sb && string(cfg.ContainerRuntime) == cr {
			return cfg
		}
	}
	t.Fatalf("no backend config matches SANDBOX_STORAGE_BACKEND=%q SANDBOX_CONTAINER_RUNTIME=%q", sb, cr)
	return common.BackendConfig{}
}

func newSandboxClient(t *testing.T, baseURL string) *transport.SandboxClient {
	t.Helper()
	cfg := transport.ClientConfig{APIVersion: "v1"}
	if tok := os.Getenv("SANDBOX_AUTH_TOKEN"); tok != "" {
		cfg.HeaderInjector = transport.HeaderTokenAuth("authorization", "Bearer "+tok)
	}
	return transport.NewSandboxClient(&http.Client{Timeout: 30 * time.Second}, baseURL, cfg)
}

func zfsBaseSnapshot() string {
	if snap := os.Getenv("SANDBOX_BASE_SNAPSHOT"); snap != "" {
		return snap
	}
	pool := os.Getenv("SANDBOX_POOL_NAME")
	if pool == "" {
		pool = "testpool"
	}
	return pool + "/bases/ubuntu-base@ready"
}

func mustCreateSession(t *testing.T, cl *transport.SandboxClient, cfg common.BackendConfig) *api.CreateSessionResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req := &api.CreateSessionRequest{
		SessionID: fmt.Sprintf("tier2-%d", time.Now().UnixNano()),
	}
	if cfg.SupportsSnapshots() {
		req.BaseSnapshot = zfsBaseSnapshot()
	}
	resp, err := cl.CreateSession.CallUnary(ctx, connect.NewRequest(req))
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	return resp.Msg
}

func mustDestroySession(t *testing.T, cl *transport.SandboxClient, sessionID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := cl.DestroySession.CallUnary(ctx, connect.NewRequest(&api.DestroySessionRequest{SessionID: sessionID})); err != nil {
		t.Fatalf("DestroySession failed: %v", err)
	}
}

func mustExecuteTool(t *testing.T, cl *transport.SandboxClient, sessionID, callID, tool string, params map[string]any) *api.ExecuteToolResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := cl.ExecuteTool.CallUnary(ctx, connect.NewRequest(&api.ExecuteToolRequest{
		SessionID:  sessionID,
		ToolCallID: callID,
		ToolName:   tool,
		Params:     params,
	}))
	if err != nil {
		t.Fatalf("ExecuteTool(%s) failed: %v", tool, err)
	}
	return resp.Msg
}

func TestDockerLifecycle_ActiveConfig(t *testing.T) {
	common.RequireDocker(t)
	cfg := activeConfig(t)
	cl := newSandboxClient(t, sandboxHostURL(t))

	resp := mustCreateSession(t, cl, cfg)
	sessionID := resp.Session.ID
	defer mustDestroySession(t, cl, sessionID)

	toolResp := mustExecuteTool(t, cl, sessionID, "lifecycle-bash", "bash", map[string]any{"cmd": "echo hello-tier2"})
	if !strings.Contains(toolResp.Content, "hello-tier2") {
		t.Fatalf("unexpected bash response content: %q", toolResp.Content)
	}
}

func TestDockerSnapshot_ActiveConfig(t *testing.T) {
	common.RequireDocker(t)
	cfg := activeConfig(t)
	cl := newSandboxClient(t, sandboxHostURL(t))

	resp := mustCreateSession(t, cl, cfg)
	sessionID := resp.Session.ID
	defer mustDestroySession(t, cl, sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	snapReq := &api.CreateSnapshotRequest{SessionID: sessionID, Name: "tier2-snap"}
	_, err := cl.CreateSnapshot.CallUnary(ctx, connect.NewRequest(snapReq))
	if cfg.SupportsSnapshots() {
		if err != nil {
			t.Fatalf("CreateSnapshot failed for snapshot-capable config: %v", err)
		}
		listResp, listErr := cl.ListSnapshots.CallUnary(ctx, connect.NewRequest(&api.ListSnapshotsRequest{SessionID: sessionID}))
		if listErr != nil {
			t.Fatalf("ListSnapshots failed: %v", listErr)
		}
		if len(listResp.Msg.Snapshots) == 0 {
			t.Fatal("expected at least one snapshot after CreateSnapshot")
		}
	} else {
		if err == nil {
			t.Fatal("CreateSnapshot succeeded for non-snapshot config")
		}
	}
}

func TestDockerTierRouting_ActiveConfig(t *testing.T) {
	common.RequireDocker(t)
	cfg := activeConfig(t)
	cl := newSandboxClient(t, sandboxHostURL(t))

	resp := mustCreateSession(t, cl, cfg)
	sessionID := resp.Session.ID
	defer mustDestroySession(t, cl, sessionID)

	_ = mustExecuteTool(t, cl, sessionID, "tier-write", "write_file", map[string]any{"path": "tier.txt", "content": "tier-value"})
	readResp := mustExecuteTool(t, cl, sessionID, "tier-read", "read_file", map[string]any{"path": "tier.txt"})
	if readResp.Tier != 1 {
		t.Fatalf("read_file tier = %d, want 1", readResp.Tier)
	}

	bashResp := mustExecuteTool(t, cl, sessionID, "tier-bash", "bash", map[string]any{"cmd": "echo tier-check"})
	if cfg.SupportsTierRouting() {
		if bashResp.Tier != 2 {
			t.Fatalf("bash tier = %d, want 2 with gvisor", bashResp.Tier)
		}
	} else {
		if bashResp.Tier != 1 {
			t.Fatalf("bash tier = %d, want 1 without gvisor", bashResp.Tier)
		}
	}
}

func TestDockerStreaming_ActiveConfig(t *testing.T) {
	common.RequireDocker(t)
	cfg := activeConfig(t)
	cl := newSandboxClient(t, sandboxHostURL(t))

	resp := mustCreateSession(t, cl, cfg)
	sessionID := resp.Session.ID
	defer mustDestroySession(t, cl, sessionID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := cl.ExecuteStream.CallServerStream(ctx, connect.NewRequest(&api.ExecuteToolRequest{
		SessionID:  sessionID,
		ToolCallID: "stream-bash",
		ToolName:   "bash",
		Params:     map[string]any{"cmd": "for i in 1 2 3; do echo $i; done"},
	}))
	if err != nil {
		t.Fatalf("ExecuteToolStream call failed: %v", err)
	}

	progressCount := 0
	var final *api.ExecuteToolResponse
	for stream.Receive() {
		msg := stream.Msg()
		if msg.Progress != nil {
			progressCount++
		}
		if msg.Response != nil {
			final = msg.Response
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("ExecuteToolStream receive error: %v", err)
	}
	if final == nil {
		t.Fatal("missing final stream response")
	}
	if !strings.Contains(final.Content, "1") {
		t.Fatalf("unexpected stream final content: %q", final.Content)
	}
	_ = progressCount
}

func TestDockerCapabilities_ActiveConfig(t *testing.T) {
	common.RequireDocker(t)
	cfg := activeConfig(t)
	cl := newSandboxClient(t, sandboxHostURL(t))

	resp := mustCreateSession(t, cl, cfg)
	sessionID := resp.Session.ID
	defer mustDestroySession(t, cl, sessionID)

	caps := resp.ServerCapabilities
	if caps.Snapshots != cfg.SupportsSnapshots() {
		t.Fatalf("caps.Snapshots = %v, want %v", caps.Snapshots, cfg.SupportsSnapshots())
	}
	if caps.Rollback != cfg.SupportsSnapshots() {
		t.Fatalf("caps.Rollback = %v, want %v", caps.Rollback, cfg.SupportsSnapshots())
	}
	if caps.TierRouting != cfg.SupportsTierRouting() {
		t.Fatalf("caps.TierRouting = %v, want %v", caps.TierRouting, cfg.SupportsTierRouting())
	}
	if !caps.StreamingProgress {
		t.Fatal("caps.StreamingProgress = false, want true")
	}
	if !caps.Pause {
		t.Fatal("caps.Pause = false, want true")
	}
}
