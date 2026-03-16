package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"flex-agent-runtime/internal/ai"
	"pgregory.net/rapid"
)

// Deferred test categories (tracked for follow-up):
// - P3: Backend parity property (local vs sandbox output equivalence)
// - S2: Callback event ordering simulation
// - SEC3: Secret redaction tests
// - O2: Differential grep oracle (vs rg)
// - ST1/ST2: Long-running soak tests (12h mixed-tool, high-fanout grep)
// - B5: Bash overhead benchmark

// =====================================================================
// P1. Path Safety Property Tests
// =====================================================================

func TestP1_PathSafetyInvariant(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()

		// Generate adversarial path fragments
		fragments := rapid.SliceOfN(
			rapid.SampledFrom([]string{
				"..", ".", "...", "~", "/", "\\",
				"etc", "passwd", "secret",
				"\x00", "\n", " ", "",
				"a/../../b", "../../../",
				"symlink", "normal.txt",
			}),
			1, 5,
		).Draw(rt, "fragments")

		path := filepath.Join(fragments...)

		resolved, err := resolveSafePath(root, path)
		if err != nil {
			// Rejection is acceptable — just verify it's a proper rejection
			return
		}

		// If accepted, the resolved path MUST be under root
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr != nil {
			rt.Fatalf("root resolution failed: %v", rootErr)
		}
		if !isUnderRoot(resolved, resolvedRoot) {
			rt.Fatalf("SAFETY VIOLATION: resolved path %q escapes root %q", resolved, resolvedRoot)
		}
	})
}

func TestP1_SymlinkEscapeProperty(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		outside := t.TempDir()

		// Create symlink pointing outside root
		linkName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "linkName")
		linkPath := filepath.Join(root, linkName)
		if err := os.Symlink(outside, linkPath); err != nil {
			rt.Skipf("symlinks not supported: %v", err)
		}

		// Any path through symlink must be rejected
		targetName := rapid.StringMatching(`[a-z]{1,5}\.txt`).Draw(rt, "target")
		_, err := resolveSafePath(root, filepath.Join(linkName, targetName))
		if err == nil {
			rt.Fatalf("SAFETY VIOLATION: symlink escape not detected for %s/%s", linkName, targetName)
		}
	})
}

// =====================================================================
// P2. Edit Correctness Property Tests
// =====================================================================

func TestP2_EditCorrectnessInvariant(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()

		// Generate random file content
		content := rapid.StringMatching(`[a-zA-Z0-9 \n]{10,200}`).Draw(rt, "content")
		if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte(content), 0o644); err != nil {
			rt.Fatalf("write: %v", err)
		}

		// Generate search string that may or may not exist
		oldStr := rapid.StringMatching(`[a-z]{1,5}`).Draw(rt, "old")
		newStr := rapid.StringMatching(`[A-Z]{1,5}`).Draw(rt, "new")

		backend := NewLocalBackend(root)
		resp, err := backend.ExecuteTool(context.Background(), ToolRequest{
			ToolName: "edit_file",
			Params: map[string]any{
				"path":       "target.txt",
				"old_string": oldStr,
				"new_string": newStr,
			},
		}, nil)
		if err != nil {
			rt.Fatalf("edit error: %v", err)
		}

		result, _ := os.ReadFile(filepath.Join(root, "target.txt"))
		resultStr := string(result)
		text := responseText(resp)

		count := strings.Count(content, oldStr)
		switch {
		case count == 0:
			// old_string absent → file must be unchanged
			if resultStr != content {
				rt.Fatalf("file modified despite absent old_string")
			}
			if !strings.Contains(text, "not found") {
				rt.Fatalf("expected not-found message, got %q", text)
			}
		case count == 1:
			// Unique match → exactly one replacement
			if !strings.Contains(resultStr, newStr) {
				rt.Fatalf("replacement not applied")
			}
			if strings.Contains(resultStr, oldStr) {
				rt.Fatalf("old_string still present after unique replacement")
			}
		case count > 1:
			// Ambiguous → file must be unchanged
			if resultStr != content {
				rt.Fatalf("file modified despite ambiguous old_string (found %d times)", count)
			}
		}
	})
}

// =====================================================================
// P4. Grep Determinism Property
// =====================================================================

func TestP4_GrepDeterminism(t *testing.T) {
	root := t.TempDir()

	// Create several files with known content
	for i := 0; i < 10; i++ {
		content := fmt.Sprintf("line_%d_alpha\nline_%d_beta\nline_%d_gamma\n", i, i, i)
		writeTestFile(t, root, fmt.Sprintf("dir%d/file%d.txt", i%3, i), content)
	}

	backend := NewLocalBackend(root)

	// Run same query 10 times and verify identical results
	var firstResult string
	for run := 0; run < 10; run++ {
		resp := execTool(t, backend, "grep", map[string]any{
			"pattern": "alpha",
		})
		text := responseText(resp)
		if run == 0 {
			firstResult = text
		} else if text != firstResult {
			t.Fatalf("non-deterministic grep on run %d:\nfirst: %q\nthis:  %q", run, firstResult, text)
		}
	}
}

// =====================================================================
// P5. Tier Classifier Stability Property
// =====================================================================

func TestP5_TierClassifierStability(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		name := rapid.SampledFrom([]string{
			"read_file", "write_file", "edit_file",
			"grep", "glob", "bash",
			"git_status", "git_diff", "git_log", "git_show",
			"git_add", "git_commit",
			"unknown_tool", "custom_tool",
		}).Draw(rt, "tool")

		// Run classifier 100 times — must always return same tier
		expected := ClassifyTool(name)
		for i := 0; i < 100; i++ {
			got := ClassifyTool(name)
			if got != expected {
				rt.Fatalf("ClassifyTool(%q) unstable: got %d then %d", name, expected, got)
			}
		}
	})
}

// =====================================================================
// F1. Atomic write correctness and failure path
// =====================================================================

func TestF1_AtomicWriteNoTempLeak(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "safe.txt", "original")

	backend := NewLocalBackend(root)
	execTool(t, backend, "write_file", map[string]any{
		"path":    "safe.txt",
		"content": "new content via atomic write",
	})

	data, err := os.ReadFile(filepath.Join(root, "safe.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content via atomic write" {
		t.Fatalf("atomic write produced unexpected result: %q", string(data))
	}

	// No temp files should remain after successful write
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
}

func TestF1_WriteToReadOnlyDirPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "readonly")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "must survive failed write"
	writeTestFile(t, root, "readonly/target.txt", original)

	// Make directory read-only so temp file creation fails
	if err := os.Chmod(subdir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(subdir, 0o755) })

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "write_file", map[string]any{
		"path":    "readonly/target.txt",
		"content": "should not be written",
	})
	text := responseText(resp)
	if !strings.Contains(text, "permission denied") && !strings.Contains(text, "read-only") {
		// Some error message about write failure is expected
		t.Logf("write to read-only dir response: %q", text)
	}

	// Original content must be preserved
	data, err := os.ReadFile(filepath.Join(subdir, "target.txt"))
	if err != nil {
		t.Fatalf("original file lost: %v", err)
	}
	if string(data) != original {
		t.Fatalf("original content corrupted: got %q, want %q", string(data), original)
	}
}

// =====================================================================
// F2. Bash process kill races
// =====================================================================

func TestF2_BashTimeoutCleanTermination(t *testing.T) {
	root := t.TempDir()
	backend := NewLocalBackend(root)

	// Run a command that would take forever, with tight timeout
	resp := execTool(t, backend, "bash", map[string]any{
		"cmd":        "while true; do echo line; sleep 0.01; done",
		"timeout_ms": float64(200),
	})
	text := responseText(resp)
	if !strings.Contains(text, "timed out") {
		t.Fatalf("expected timeout, got %q", text)
	}
	if resp.ExitCode == nil {
		t.Fatal("expected exit code on timeout")
	}
}

// =====================================================================
// F3. Sandbox RPC intermittent failures
// =====================================================================

func TestF3_EnvironmentExecuteFailure(t *testing.T) {
	fakeExecute := func(_ context.Context, _ ToolRequest, _ func(ToolProgress)) (*ToolResponse, error) {
		return nil, fmt.Errorf("connection reset by peer")
	}

	envTools := NewEnvironmentTools(fakeExecute)
	for _, tool := range envTools {
		if tool.Name == "read_file" {
			result, err := tool.Execute(context.Background(), "tc-1", map[string]any{"path": "test.txt"}, nil)
			if err == nil {
				t.Fatal("expected error from environment execute failure")
			}
			if !result.IsError {
				t.Fatal("expected IsError=true on failure")
			}
			return
		}
	}
	t.Fatal("read_file not found")
}

// =====================================================================
// F4. Massive-output truncation
// =====================================================================

func TestF4_BashOutputTruncation(t *testing.T) {
	root := t.TempDir()
	backend := NewLocalBackend(root)

	// Generate output larger than maxBashOutput (256KB)
	resp := execTool(t, backend, "bash", map[string]any{
		"cmd": "dd if=/dev/zero bs=1024 count=512 2>/dev/null | tr '\\0' 'A'",
	})
	text := responseText(resp)
	if !strings.Contains(text, "truncated") {
		t.Fatalf("expected truncation for large output, got %d bytes", len(text))
	}
	// Output should be bounded
	if len(text) > maxBashOutput*2 { // allow room for metadata
		t.Fatalf("output not properly bounded: %d bytes", len(text))
	}
}

func TestF4_GrepTruncation(t *testing.T) {
	root := t.TempDir()

	// Create file with many matching lines
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, fmt.Sprintf("match_line_%d", i))
	}
	writeTestFile(t, root, "huge.txt", strings.Join(lines, "\n"))

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{
		"pattern":     "match_line",
		"max_results": float64(10),
	})
	text := responseText(resp)
	if !strings.Contains(text, "truncated at 10") {
		t.Fatalf("expected truncation, got %q", text)
	}
}

func TestF4_ReadFileSizeLimit(t *testing.T) {
	root := t.TempDir()

	// Create file slightly over the 10MB limit
	bigFile := filepath.Join(root, "big.bin")
	f, err := os.Create(bigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(11 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "big.bin"})
	text := responseText(resp)
	if !strings.Contains(text, "too large") {
		t.Fatalf("expected too-large error, got %q", text)
	}
}

// =====================================================================
// F5. Concurrent tool calls
// =====================================================================

func TestF5_ConcurrentToolCalls(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "shared.txt", "initial content")

	backend := NewLocalBackend(root)
	var wg sync.WaitGroup
	errCh := make(chan error, 100)

	// Launch concurrent reads
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := backend.ExecuteTool(context.Background(), ToolRequest{
				ToolName: "read_file",
				Params:   map[string]any{"path": "shared.txt"},
			}, nil)
			if err != nil {
				errCh <- err
			}
		}()
	}

	// Launch concurrent writes to different files
	for i := 0; i < 10; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			_, err := backend.ExecuteTool(context.Background(), ToolRequest{
				ToolName: "write_file",
				Params: map[string]any{
					"path":    fmt.Sprintf("concurrent_%d.txt", i),
					"content": fmt.Sprintf("content_%d", i),
				},
			}, nil)
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent tool call error: %v", err)
	}

	// Verify all files written correctly
	for i := 0; i < 10; i++ {
		data, err := os.ReadFile(filepath.Join(root, fmt.Sprintf("concurrent_%d.txt", i)))
		if err != nil {
			t.Errorf("missing concurrent file %d: %v", i, err)
			continue
		}
		expected := fmt.Sprintf("content_%d", i)
		if string(data) != expected {
			t.Errorf("concurrent file %d content mismatch: got %q", i, string(data))
		}
	}
}

// =====================================================================
// S1. Tier Routing Simulation
// =====================================================================

func TestS1_TierRoutingSimulation(t *testing.T) {
	type toolTier struct {
		name string
		tier ToolTier
	}
	expected := []toolTier{
		{"read_file", Tier1},
		{"write_file", Tier1},
		{"edit_file", Tier1},
		{"grep", Tier1},
		{"glob", Tier1},
		{"bash", Tier2},
		{"git_status", Tier1},
		{"git_diff", Tier1},
		{"git_log", Tier1},
		{"git_show", Tier1},
		{"git_add", Tier2},
		{"git_commit", Tier2},
	}

	for _, tt := range expected {
		got := ClassifyTool(tt.name)
		if got != tt.tier {
			t.Errorf("ClassifyTool(%q) = Tier%d, want Tier%d", tt.name, got, tt.tier)
		}
	}
}

// =====================================================================
// S3. Snapshot Metadata Propagation
// =====================================================================

func TestS3_SnapshotMetadataPropagation(t *testing.T) {
	snapshotID := "snap-tool-tc1-20260313"
	exitCode := 0
	fakeExecute := func(_ context.Context, _ ToolRequest, _ func(ToolProgress)) (*ToolResponse, error) {
		return &ToolResponse{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "result"}},
			SnapshotID: snapshotID,
			ExitCode:   &exitCode,
		}, nil
	}

	tools := NewEnvironmentTools(fakeExecute)
	for _, tool := range tools {
		if tool.Name == "read_file" {
			result, err := tool.Execute(context.Background(), "tc-1", map[string]any{"path": "x.txt"}, nil)
			if err != nil {
				t.Fatalf("execute error: %v", err)
			}
			if result.SnapshotID != snapshotID {
				t.Fatalf("expected SnapshotID %q, got %q", snapshotID, result.SnapshotID)
			}
			if result.ExitCode == nil || *result.ExitCode != 0 {
				t.Fatalf("expected ExitCode 0, got %v", result.ExitCode)
			}
			return
		}
	}
	t.Fatal("read_file not found")
}

// =====================================================================
// SEC1. Path traversal + symlink escape (security)
// =====================================================================

func TestSEC1_AdversarialPaths(t *testing.T) {
	root := t.TempDir()
	backend := NewLocalBackend(root)

	adversarialPaths := []string{
		"../../../etc/passwd",
		"..\\..\\..\\windows\\system32\\config\\sam",
		"foo/../../../../../../etc/shadow",
		"/etc/passwd",
		"~/.ssh/id_rsa",
		"foo\x00bar",
		strings.Repeat("a/", 200) + "../../../etc/passwd",
	}

	for _, path := range adversarialPaths {
		if !utf8.ValidString(path) {
			continue // skip invalid UTF-8 for tool params
		}
		resp := execTool(t, backend, "read_file", map[string]any{"path": path})
		text := responseText(resp)
		// Should be rejected — not returning actual file content
		if strings.Contains(text, "root:") { // /etc/passwd content
			t.Fatalf("SECURITY: path traversal succeeded for %q", path)
		}
	}
}

func TestSEC1_SymlinkChainEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, outside, "secret.txt", "TOP_SECRET")

	// Create chain: link1 -> link2 -> outside
	intermediate := t.TempDir()
	os.Symlink(outside, filepath.Join(intermediate, "final"))
	os.Symlink(intermediate, filepath.Join(root, "link1"))

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "link1/final/secret.txt"})
	text := responseText(resp)
	if strings.Contains(text, "TOP_SECRET") {
		t.Fatal("SECURITY: symlink chain escape not detected")
	}
}

// =====================================================================
// SEC2. Command injection boundary
// =====================================================================

func TestSEC2_BashNoUnintendedExpansion(t *testing.T) {
	root := t.TempDir()
	backend := NewLocalBackend(root)

	// The command is passed as a single string to sh -c, so shell expansion
	// is expected within the command. What we verify is that the tool correctly
	// captures output and doesn't leak environment or state.
	resp := execTool(t, backend, "bash", map[string]any{
		"cmd": "echo 'hello; echo injected'",
	})
	text := responseText(resp)
	// The single quotes should prevent the semicolon from being interpreted
	if strings.Contains(text, "injected") && !strings.Contains(text, "hello; echo injected") {
		t.Fatalf("unexpected command injection behavior: %q", text)
	}
}

// =====================================================================
// SEC4. Resource exhaustion guards
// =====================================================================

func TestSEC4_ExtremeRegexPattern(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "test.txt", strings.Repeat("a", 1000))

	backend := NewLocalBackend(root)

	// This regex is valid but should complete in reasonable time
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := backend.ExecuteTool(ctx, ToolRequest{
		ToolName: "grep",
		Params:   map[string]any{"pattern": "a{1,100}"},
	}, nil)
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		t.Fatal("regex pattern caused timeout — resource exhaustion")
	}
}

// =====================================================================
// O1. Golden Output Corpus
// =====================================================================

func TestO1_ReadFileOutputFormat(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "golden.txt", "line1\nline2\nline3\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "golden.txt"})
	text := responseText(resp)

	// Verify cat -n style format: right-justified 6-char line numbers + tab
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	for i, line := range lines[:3] {
		expected := fmt.Sprintf("%6d\tline%d", i+1, i+1)
		if !strings.HasPrefix(line, expected) {
			t.Errorf("line %d format mismatch: got %q, want prefix %q", i+1, line, expected)
		}
	}
}

func TestO1_GrepOutputFormat(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "search.txt", "hello world\nfoo bar\nhello again\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{"pattern": "hello"})
	text := responseText(resp)

	// Verify file:line:col: content format
	if !strings.Contains(text, "search.txt:1:1: hello world") {
		t.Fatalf("grep output format mismatch: %q", text)
	}
	if !strings.Contains(text, "search.txt:3:1: hello again") {
		t.Fatalf("grep output missing second match: %q", text)
	}
}

func TestO1_EditSuccessFormat(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "edit.txt", "old value here")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":       "edit.txt",
		"old_string": "old value",
		"new_string": "new value",
	})
	text := responseText(resp)
	if !strings.Contains(text, "1 replacement") {
		t.Fatalf("edit success format mismatch: %q", text)
	}
}

func TestO1_BashOutputFormat(t *testing.T) {
	root := t.TempDir()
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "bash", map[string]any{"cmd": "echo test"})
	text := responseText(resp)

	// Must contain exit code and duration
	if !strings.Contains(text, "Exit code: 0") {
		t.Fatalf("bash output missing exit code: %q", text)
	}
	if !strings.Contains(text, "Duration:") {
		t.Fatalf("bash output missing duration: %q", text)
	}
}

// =====================================================================
// B1-B5. Benchmarks
// =====================================================================

func BenchmarkB1_ReadFile(b *testing.B) {
	root := b.TempDir()
	// Create 1MB file
	data := strings.Repeat("benchmark line content here\n", 35000)
	writeTestFileB(b, root, "bench.txt", data)

	backend := NewLocalBackend(root)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := backend.ExecuteTool(context.Background(), ToolRequest{
			ToolName: "read_file",
			Params:   map[string]any{"path": "bench.txt"},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkB2_EditFile(b *testing.B) {
	root := b.TempDir()
	content := strings.Repeat("line of content\n", 60000) // ~1MB
	original := content

	backend := NewLocalBackend(root)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Reset file each iteration
		os.WriteFile(filepath.Join(root, "bench_edit.txt"), []byte(original), 0o644)

		_, err := backend.ExecuteTool(context.Background(), ToolRequest{
			ToolName: "edit_file",
			Params: map[string]any{
				"path":       "bench_edit.txt",
				"old_string": "line of content\nline of content\nline of content",
				"new_string": "REPLACED\nREPLACED\nREPLACED",
			},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkB3_GrepLiteral(b *testing.B) {
	root := b.TempDir()
	// Create files totaling ~1MB
	for i := 0; i < 10; i++ {
		content := strings.Repeat(fmt.Sprintf("line %d of file %d\n", i, i), 7000)
		writeTestFileB(b, root, fmt.Sprintf("bench%d.txt", i), content)
	}

	backend := NewLocalBackend(root)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := backend.ExecuteTool(context.Background(), ToolRequest{
			ToolName: "grep",
			Params: map[string]any{
				"pattern": "line 5 of file 5",
				"literal": true,
			},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkB3_GrepRegex(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 10; i++ {
		content := strings.Repeat(fmt.Sprintf("line %d of file %d\n", i, i), 7000)
		writeTestFileB(b, root, fmt.Sprintf("bench%d.txt", i), content)
	}

	backend := NewLocalBackend(root)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := backend.ExecuteTool(context.Background(), ToolRequest{
			ToolName: "grep",
			Params:   map[string]any{"pattern": "line \\d+ of file [0-9]"},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkB4_GlobLargeTree(b *testing.B) {
	root := b.TempDir()
	// Create tree with many files
	for i := 0; i < 100; i++ {
		for j := 0; j < 10; j++ {
			writeTestFileB(b, root, fmt.Sprintf("dir%d/sub%d/file%d.go", i, j, j), "x")
		}
	}

	backend := NewLocalBackend(root)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := backend.ExecuteTool(context.Background(), ToolRequest{
			ToolName: "glob",
			Params:   map[string]any{"pattern": "**/*.go"},
		}, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// =====================================================================
// O3. Edit Operation Oracle
// =====================================================================

func TestO3_EditOracleConsistency(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		old        string
		new        string
		replaceAll bool
		expected   string
	}{
		{"simple", "hello world", "world", "earth", false, "hello earth"},
		{"multiple_unique", "a b c", "b", "B", false, "a B c"},
		{"replace_all", "aXbXc", "X", "Y", true, "aYbYc"},
		{"no_match", "hello", "xyz", "abc", false, "hello"},
		{"empty_new", "hello world", "world", "", false, "hello "},
		{"multiline", "line1\nline2\nline3", "line2", "LINE2", false, "line1\nLINE2\nline3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "oracle.txt", tc.content)

			backend := NewLocalBackend(root)
			params := map[string]any{
				"path":       "oracle.txt",
				"old_string": tc.old,
				"new_string": tc.new,
			}
			if tc.replaceAll {
				params["replace_all"] = true
			}

			execTool(t, backend, "edit_file", params)

			data, err := os.ReadFile(filepath.Join(root, "oracle.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.expected {
				t.Fatalf("oracle mismatch:\ngot:  %q\nwant: %q", string(data), tc.expected)
			}
		})
	}
}

// =====================================================================
// ST3. Git command stress
// =====================================================================

func TestST3_GitCommandStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	root := t.TempDir()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}

	// Create initial commit
	writeTestFile(t, root, "stress.txt", "initial")
	gitExec(t, root, "add", ".")
	gitExec(t, root, "commit", "-m", "initial")

	backend := NewLocalBackend(root)

	// Rapid cycles of modify → status → diff → add → commit
	for i := 0; i < 20; i++ {
		writeTestFile(t, root, "stress.txt", fmt.Sprintf("iteration %d", i))

		resp := execTool(t, backend, "git_status", map[string]any{})
		text := responseText(resp)
		if !strings.Contains(text, "stress.txt") {
			t.Fatalf("iteration %d: status should show modified file, got %q", i, text)
		}

		execTool(t, backend, "git_diff", map[string]any{})
		execTool(t, backend, "git_add", map[string]any{"paths": []any{"stress.txt"}})
		execTool(t, backend, "git_commit", map[string]any{
			"message": fmt.Sprintf("stress iteration %d", i),
		})
	}

	// Verify log shows all commits
	resp := execTool(t, backend, "git_log", map[string]any{"n": float64(25)})
	text := responseText(resp)
	if !strings.Contains(text, "stress iteration 19") {
		t.Fatalf("log missing final stress commit: %q", text)
	}
}

// =====================================================================
// Helpers
// =====================================================================

func writeTestFileB(b *testing.B, dir, name, content string) {
	b.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
