//go:build docker

// Package tier2 contains Docker-based E2E tests that require a running
// sandbox-host container accessible via RPC. These tests exercise all four
// backend configurations (C1-C4) against a real sandboxed environment served
// by the sandbox-host binary. The SANDBOX_HOST_URL environment variable must
// point to the running container's RPC endpoint.
//
// Run with: go test -tags=docker ./tests/external/tier2/...
package tier2

import (
	"os"
	"testing"

	"h2-agent-runtime/tests/external/common"
)

// sandboxHostURL returns the sandbox-host RPC endpoint from the environment,
// failing the test if it is not configured.
func sandboxHostURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SANDBOX_HOST_URL")
	if url == "" {
		t.Fatal("SANDBOX_HOST_URL not set")
	}
	return url
}

// TestDockerLifecycle_AllConfigs verifies session create/execute/destroy across
// all four backend configurations (C1-C4). Each subtest connects to the
// sandbox-host via RPC, creates a session, executes a simple tool, verifies the
// response, and then destroys the session.
func TestDockerLifecycle_AllConfigs(t *testing.T) {
	common.RequireDocker(t)
	hostURL := sandboxHostURL(t)

	for _, cfg := range common.StandardConfigs() {
		cfg := cfg
		t.Run(cfg.Name, func(t *testing.T) {
			t.Parallel()

			// Skip configs whose hardware requirements are not met.
			if cfg.SupportsSnapshots() {
				common.RequireZFSBackend(t)
			}
			if cfg.SupportsTierRouting() {
				common.RequireGVisorRuntime(t)
			}

			// TODO: implement when sandbox-host binary is available.
			//
			// Steps:
			//   1. Dial the RPC client at hostURL.
			//   2. Call NewNativeSandboxEnvironment(client, NativeSandboxConfig{
			//          StorageBackend:   cfg.StorageBackend,
			//          ContainerRuntime: cfg.ContainerRuntime,
			//      }, logger).
			//   3. env.Create(ctx, environment.SessionConfig{SessionID: uuid.New().String()}).
			//   4. env.ExecuteTool(ctx, ToolRequest{ToolName: "echo", Params: {"text": "hello"}}, nil).
			//   5. Assert response content contains "hello".
			//   6. env.Destroy(ctx) — assert no error.
			_ = hostURL
			t.Skip("TODO: implement when sandbox-host binary is available")
		})
	}
}

// TestDockerSnapshot_ZFSConfigs verifies snapshot and rollback semantics using
// real ZFS-backed sessions (configs C1 and C2). A file is written pre-snapshot,
// modified post-snapshot, and then the session is rolled back; the test asserts
// that the post-snapshot write is absent after rollback.
func TestDockerSnapshot_ZFSConfigs(t *testing.T) {
	common.RequireDocker(t)
	common.RequireZFSBackend(t)
	hostURL := sandboxHostURL(t)

	zfsConfigs := []common.BackendConfig{}
	for _, cfg := range common.StandardConfigs() {
		if cfg.SupportsSnapshots() {
			zfsConfigs = append(zfsConfigs, cfg)
		}
	}

	for _, cfg := range zfsConfigs {
		cfg := cfg
		t.Run(cfg.Name, func(t *testing.T) {
			t.Parallel()

			// TODO: implement when sandbox-host binary is available.
			//
			// Steps:
			//   1. Dial the RPC client at hostURL.
			//   2. Create a ZFS-backed session.
			//   3. ExecuteTool: write /workspace/before.txt with content "before".
			//   4. env.CreateSnapshot(ctx, "snap1").
			//   5. ExecuteTool: write /workspace/after.txt with content "after".
			//   6. env.Rollback(ctx, snap1.ID).
			//   7. Assert /workspace/before.txt still exists with "before".
			//   8. Assert /workspace/after.txt is absent (rolled back).
			//   9. env.Destroy(ctx).
			_ = hostURL
			t.Skip("TODO: implement when sandbox-host binary is available")
		})
	}
}

// TestDockerTierRouting_GVisorConfigs verifies that tool calls are routed to
// the correct execution tier when gVisor is active (configs C1 and C3). The
// test executes a mix of Tier 1 (trusted) and Tier 2 (sandboxed) tools and
// asserts that tier-routing metadata in the responses matches expectations.
func TestDockerTierRouting_GVisorConfigs(t *testing.T) {
	common.RequireDocker(t)
	common.RequireGVisorRuntime(t)
	hostURL := sandboxHostURL(t)

	gvisorConfigs := []common.BackendConfig{}
	for _, cfg := range common.StandardConfigs() {
		if cfg.SupportsTierRouting() {
			gvisorConfigs = append(gvisorConfigs, cfg)
		}
	}

	for _, cfg := range gvisorConfigs {
		cfg := cfg
		t.Run(cfg.Name, func(t *testing.T) {
			t.Parallel()

			// TODO: implement when sandbox-host binary is available.
			//
			// Steps:
			//   1. Dial the RPC client at hostURL.
			//   2. Create a gVisor-backed session.
			//   3. ExecuteTool: call a Tier 1 tool (e.g. "read_file").
			//      Assert the response indicates tier=1 (runs in host process).
			//   4. ExecuteTool: call a Tier 2 tool (e.g. "bash").
			//      Assert the response indicates tier=2 (runs in gVisor container).
			//   5. env.Destroy(ctx).
			_ = hostURL
			t.Skip("TODO: implement when sandbox-host binary is available")
		})
	}
}

// TestDockerStreamingProgress verifies that a long-running tool (e.g. bash)
// emits intermediate progress events before the final response arrives. This
// test can run against any config; it picks the first available configuration.
func TestDockerStreamingProgress(t *testing.T) {
	common.RequireDocker(t)
	hostURL := sandboxHostURL(t)

	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Dial the RPC client at hostURL.
	//   2. Create a session using the minimal config (C4).
	//   3. ExecuteTool: call "bash" with a command that prints several lines
	//      over a few seconds, e.g. "for i in 1 2 3; do echo $i; sleep 0.1; done".
	//      Supply an onProgress callback that appends each ToolProgress to a slice.
	//   4. After ExecuteTool returns, assert len(progressEvents) > 0.
	//   5. Assert the final ToolResponse content contains the expected output.
	//   6. env.Destroy(ctx).
	_ = hostURL
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestDockerCapabilities_AllConfigs verifies that the capabilities reported by
// each configuration match the expected values for that backend. For example,
// ZFS-backed configs must report Snapshots=true and Rollback=true; gVisor
// configs must report TierRouting=true.
func TestDockerCapabilities_AllConfigs(t *testing.T) {
	common.RequireDocker(t)
	hostURL := sandboxHostURL(t)

	for _, cfg := range common.StandardConfigs() {
		cfg := cfg
		t.Run(cfg.Name, func(t *testing.T) {
			t.Parallel()

			// Skip configs whose hardware requirements are not met.
			if cfg.SupportsSnapshots() {
				common.RequireZFSBackend(t)
			}
			if cfg.SupportsTierRouting() {
				common.RequireGVisorRuntime(t)
			}

			// TODO: implement when sandbox-host binary is available.
			//
			// Steps:
			//   1. Dial the RPC client at hostURL.
			//   2. Create a NativeSandboxEnvironment with cfg.StorageBackend and
			//      cfg.ContainerRuntime.
			//   3. Call env.Capabilities().
			//   4. Assert caps.Snapshots == cfg.SupportsSnapshots().
			//   5. Assert caps.Rollback  == cfg.SupportsSnapshots().
			//   6. Assert caps.TierRouting == cfg.SupportsTierRouting().
			//   7. Assert caps.StreamingProgress == true (always supported).
			_ = hostURL
			t.Skip("TODO: implement when sandbox-host binary is available")
		})
	}
}
