//go:build native

// Package tier3 contains compose-backed external E2E tests that require a
// dedicated Linux machine with real ZFS, gVisor (runsc), and optionally live
// provider API keys. The test harness is gated by the "native" build tag and
// is expected to run with the full compose profile so sandbox-host is available.
//
// Run with: make test-external-tier3
package tier3

import (
	"os"
	"testing"

	"h2-agent-runtime/tests/external/common"
)

// TestNativeZFSPool verifies end-to-end ZFS pool operations using a real
// sandbox-host session backed by ZFS. It creates a session, performs a
// snapshot, modifies state, rolls back, and asserts the rollback is correct
// using real ZFS dataset inspection via the host CLI.
func TestNativeZFSPool(t *testing.T) {
	common.RequireZFS(t)

	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Start (or connect to) a sandbox-host configured with StorageBackendZFS.
	//   2. Create a session and write /workspace/v1.txt = "version1".
	//   3. CreateSnapshot(ctx, "snap-v1").
	//   4. Write /workspace/v2.txt = "version2".
	//   5. Run `zfs list -t snapshot` on the host to confirm the snapshot exists.
	//   6. Rollback(ctx, snap-v1.ID).
	//   7. Assert /workspace/v2.txt is absent.
	//   8. Assert /workspace/v1.txt contains "version1".
	//   9. Run `zfs list` again to confirm the post-rollback dataset state.
	//  10. Destroy session, assert the ZFS dataset is cleaned up.
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestNativeGVisorIsolation verifies that bash commands executed in a gVisor
// container observe a distinct PID namespace from the host, providing evidence
// that kernel-level isolation is active.
func TestNativeGVisorIsolation(t *testing.T) {
	common.RequireGVisor(t)

	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Start (or connect to) a sandbox-host configured with ContainerRuntimeGVisor.
	//   2. Create a session.
	//   3. ExecuteTool: bash with cmd="cat /proc/1/cmdline" (reads PID 1 in the container).
	//   4. Assert the result is NOT the host's init/systemd — it should be the
	//      gVisor sentry or a container init, confirming namespace isolation.
	//   5. ExecuteTool: bash with cmd="ls /proc | wc -l" to count visible PIDs.
	//   6. Assert the PID count is much smaller than the host's PID count,
	//      confirming PID namespace isolation.
	//   7. Destroy session.
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestNativeProviderIntegration_Anthropic runs a simple single-turn agent
// scenario against the real Anthropic API. The test fails if the
// ANTHROPIC_API_KEY environment variable is not set.
func TestNativeProviderIntegration_Anthropic(t *testing.T) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Fatal("ANTHROPIC_API_KEY not set")
	}

	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Build an agent with the Anthropic provider (claude-3-haiku or
	//      claude-3-5-haiku for low cost) and a LocalEnvironment.
	//   2. Prompt: "Reply with only the word PONG."
	//   3. Wait for EventTurnCompleted (timeout 30s).
	//   4. Assert the final assistant message contains "PONG".
	//   5. Assert ProviderCalls == 1.
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestNativeProviderIntegration_OpenAI runs a simple single-turn agent
// scenario against the real OpenAI API. The test fails if the
// OPENAI_API_KEY environment variable is not set.
func TestNativeProviderIntegration_OpenAI(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Fatal("OPENAI_API_KEY not set")
	}

	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Build an agent with the OpenAI provider (gpt-4o-mini for low cost)
	//      and a LocalEnvironment.
	//   2. Prompt: "Reply with only the word PONG."
	//   3. Wait for EventTurnCompleted (timeout 30s).
	//   4. Assert the final assistant message contains "PONG".
	//   5. Assert ProviderCalls == 1.
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestNativeMultiSessionStress creates 10 concurrent ZFS+gVisor sessions and
// runs a parallel agent scenario in each, verifying that all sessions complete
// successfully without data bleed-over between them.
func TestNativeMultiSessionStress(t *testing.T) {
	common.RequireZFS(t)
	common.RequireGVisor(t)

	const sessionCount = 10

	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Start (or connect to) a sandbox-host with ZFS + gVisor.
	//   2. Concurrently create sessionCount sessions (C1 config).
	//   3. In each session goroutine:
	//      a. Write /workspace/id.txt = the session's unique ID.
	//      b. ExecuteTool: bash with cmd="cat /workspace/id.txt".
	//      c. Assert the output matches only that session's ID (no bleed-over).
	//      d. CreateSnapshot, write a different file, Rollback.
	//      e. Assert the post-rollback file is absent.
	//      f. Destroy the session.
	//   4. Wait for all goroutines to finish; assert no errors.
	_ = sessionCount
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestNativeFailure_SandboxHostCrash verifies that the agent handles a
// mid-execution sandbox-host crash gracefully: the RPC call returns an error,
// and the agent emits an appropriate error event rather than hanging.
func TestNativeFailure_SandboxHostCrash(t *testing.T) {
	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Start sandbox-host as a subprocess (os/exec).
	//   2. Create a NativeSandboxEnvironment connected to it.
	//   3. Begin an ExecuteTool call for a long-running bash command.
	//   4. In a goroutine, wait 200ms then kill the sandbox-host process.
	//   5. Assert that ExecuteTool returns a non-nil error (not a hang).
	//   6. If testing via an agent: assert an EventProviderError or
	//      EventToolCompleted with an error result is emitted within 5s.
	t.Skip("TODO: implement when sandbox-host binary is available")
}

// TestNativeFailure_DoubleDestroy verifies that calling Destroy() on an already
// destroyed session is idempotent and returns no error on the second call.
func TestNativeFailure_DoubleDestroy(t *testing.T) {
	// TODO: implement when sandbox-host binary is available.
	//
	// Steps:
	//   1. Start (or connect to) a sandbox-host.
	//   2. Create a session (any config).
	//   3. Call env.Destroy(ctx) — assert no error.
	//   4. Call env.Destroy(ctx) again — assert no error (idempotent).
	//   5. Optionally confirm via RPC GetSession that the session no longer exists.
	t.Skip("TODO: implement when sandbox-host binary is available")
}
