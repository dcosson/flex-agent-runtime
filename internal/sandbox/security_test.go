package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SEC1. Session Isolation
func TestSEC1_SessionIsolation(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess1 := createTestSession(t, svc, "isolated-1")
	sess2 := createTestSession(t, svc, "isolated-2")

	// Write a file only in session 1's mountpoint
	s1, _ := svc.getSession(sess1.ID)
	s1.mu.RLock()
	mp1 := s1.mountpoint
	s1.mu.RUnlock()
	if err := os.WriteFile(filepath.Join(mp1, "secret.txt"), []byte("session1-secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session 2 should not be able to read session 1's files via path traversal
	s2, _ := svc.getSession(sess2.ID)
	s2.mu.RLock()
	mp2 := s2.mountpoint
	s2.mu.RUnlock()

	// Try path traversal from session 2 to session 1
	relPath, err := filepath.Rel(mp2, filepath.Join(mp1, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.ExecuteTool(ctx, ExecuteToolRequest{
		SessionID: sess2.ID,
		ToolName:  "read_file",
		Params:    map[string]any{"path": relPath},
	})
	// The path traversal should be caught by validatePath
	if err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if !strings.Contains(err.Error(), "path escape") {
		t.Fatalf("expected path escape error, got: %v", err)
	}
}

// SEC1b. Direct path traversal with ../ patterns
func TestSEC1b_PathTraversalPatterns(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "traversal")

	traversalPaths := []string{
		"../../etc/passwd",
		"../../../etc/shadow",
		"/etc/passwd",
		"test/../../etc/passwd",
		"./../../etc/passwd",
	}

	for _, p := range traversalPaths {
		t.Run(p, func(t *testing.T) {
			_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
				SessionID: sess.ID,
				ToolName:  "read_file",
				Params:    map[string]any{"path": p},
			})
			if err == nil {
				t.Fatalf("path %q should be rejected", p)
			}
		})
	}
}

// SEC2. Session ID Injection
func TestSEC2_SessionIDInjection(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	malicious := []string{
		"../../../etc",
		"sess; rm -rf /",
		"sess\x00extra",
		"sess@snap",
		"../../pool/bases/test",
		"sess/../../etc",
		strings.Repeat("a", 1000),
	}

	for _, id := range malicious {
		t.Run(id, func(t *testing.T) {
			_, err := svc.CreateSession(ctx, CreateSessionRequest{
				BaseSnapshot: "pool/bases/test@v1",
				SessionID:    id,
			})
			if err == nil {
				// If it somehow succeeded, clean up
				svc.DestroySession(ctx, id)
				t.Fatalf("session with malicious ID %q should be rejected", id)
			}
		})
	}
}

// SEC3. Tool Parameter Injection
func TestSEC3_ToolParameterInjection(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	sess := createTestSession(t, svc, "param-inject")

	t.Run("bash_empty_cmd", func(t *testing.T) {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "bash",
			Params:    map[string]any{"cmd": ""},
		})
		if err == nil {
			t.Fatal("empty bash cmd should be rejected")
		}
	})

	t.Run("bash_whitespace_cmd", func(t *testing.T) {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "bash",
			Params:    map[string]any{"cmd": "   "},
		})
		if err == nil {
			t.Fatal("whitespace-only bash cmd should be rejected")
		}
	})

	t.Run("git_commit_empty_message", func(t *testing.T) {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "git_commit",
			Params:    map[string]any{"message": ""},
		})
		if err == nil {
			t.Fatal("empty git commit message should be rejected")
		}
	})

	t.Run("read_file_path_traversal", func(t *testing.T) {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "read_file",
			Params:    map[string]any{"path": "../../../etc/passwd"},
		})
		if err == nil {
			t.Fatal("path traversal in read_file should be rejected")
		}
	})

	t.Run("write_file_path_traversal", func(t *testing.T) {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "write_file",
			Params:    map[string]any{"file_path": "../../../tmp/evil", "content": "pwned"},
		})
		if err == nil {
			t.Fatal("path traversal in write_file should be rejected")
		}
	})

	t.Run("unknown_tier2_tool", func(t *testing.T) {
		_, err := svc.ExecuteTool(ctx, ExecuteToolRequest{
			SessionID: sess.ID,
			ToolName:  "delete_everything",
			Params:    map[string]any{},
		})
		if err == nil {
			t.Fatal("unknown tier 2 tool should be rejected")
		}
	})
}
