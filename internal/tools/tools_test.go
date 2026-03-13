package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2-agent-runtime/internal/ai"
)

// --- Helper ---

func tmpWorkspace(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func execTool(t *testing.T, backend *LocalBackend, name string, params map[string]any) *ToolResponse {
	t.Helper()
	resp, err := backend.ExecuteTool(context.Background(), ToolRequest{
		ToolName: name,
		Params:   params,
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteTool(%s) error: %v", name, err)
	}
	return resp
}

func responseText(resp *ToolResponse) string {
	if resp == nil || len(resp.Content) == 0 {
		return ""
	}
	if tc, ok := resp.Content[0].(*ai.TextContent); ok {
		return tc.Text
	}
	return ""
}

// =====================================================================
// Tier Classifier
// =====================================================================

func TestClassifyTool(t *testing.T) {
	tier1 := []string{"read_file", "write_file", "edit_file", "grep", "glob",
		"git_status", "git_diff", "git_log", "git_show"}
	for _, name := range tier1 {
		if got := ClassifyTool(name); got != Tier1 {
			t.Errorf("ClassifyTool(%q) = %d, want Tier1", name, got)
		}
	}

	tier2 := []string{"bash", "git_add", "git_commit"}
	for _, name := range tier2 {
		if got := ClassifyTool(name); got != Tier2 {
			t.Errorf("ClassifyTool(%q) = %d, want Tier2", name, got)
		}
	}

	// Unknown tools default to Tier2
	if got := ClassifyTool("unknown_tool"); got != Tier2 {
		t.Errorf("ClassifyTool(unknown_tool) = %d, want Tier2", got)
	}
}

// =====================================================================
// Path Safety
// =====================================================================

func TestResolveSafePath_Valid(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "hello.txt", "hi")

	resolved, err := resolveSafePath(root, "hello.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resolved, "hello.txt") {
		t.Fatalf("expected hello.txt suffix, got %q", resolved)
	}
}

func TestResolveSafePath_AbsoluteWithinRoot(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "abs.txt", "data")

	resolved, err := resolveSafePath(root, filepath.Join(root, "abs.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resolved, "abs.txt") {
		t.Fatalf("expected abs.txt suffix, got %q", resolved)
	}
}

func TestResolveSafePath_TraversalRejected(t *testing.T) {
	root := tmpWorkspace(t)
	_, err := resolveSafePath(root, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "escapes workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveSafePath_SymlinkEscape(t *testing.T) {
	root := tmpWorkspace(t)
	outside := t.TempDir()
	writeTestFile(t, outside, "secret.txt", "secret")

	linkPath := filepath.Join(root, "escape")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	_, err := resolveSafePath(root, "escape/secret.txt")
	if err == nil {
		t.Fatal("expected error for symlink escape")
	}
}

func TestResolveSafePath_EmptyPath(t *testing.T) {
	root := tmpWorkspace(t)
	_, err := resolveSafePath(root, "")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestResolveSafePath_NewFile(t *testing.T) {
	root := tmpWorkspace(t)
	resolved, err := resolveSafePath(root, "newfile.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resolved, "newfile.txt") {
		t.Fatalf("expected newfile.txt suffix, got %q", resolved)
	}
}

// =====================================================================
// read_file
// =====================================================================

func TestReadFile_Basic(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "test.txt", "line1\nline2\nline3\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "test.txt"})
	text := responseText(resp)
	if !strings.Contains(text, "line1") || !strings.Contains(text, "line2") {
		t.Fatalf("expected file content, got %q", text)
	}
	if !strings.Contains(text, "1\t") {
		t.Fatalf("expected line numbers, got %q", text)
	}
}

func TestReadFile_WithOffset(t *testing.T) {
	root := tmpWorkspace(t)
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = strings.Repeat("x", 10)
	}
	writeTestFile(t, root, "big.txt", strings.Join(lines, "\n"))

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{
		"path":   "big.txt",
		"offset": float64(50),
		"limit":  float64(10),
	})
	text := responseText(resp)
	if !strings.Contains(text, "51\t") {
		t.Fatalf("expected line 51, got %q", text)
	}
	if !strings.Contains(text, "showing lines") {
		t.Fatalf("expected truncation note, got %q", text)
	}
}

func TestReadFile_MissingPath(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "nope.txt"})
	text := responseText(resp)
	if !strings.Contains(text, "file not found") {
		t.Fatalf("expected not found error, got %q", text)
	}
}

func TestReadFile_Directory(t *testing.T) {
	root := tmpWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "subdir"})
	text := responseText(resp)
	if !strings.Contains(text, "directory") {
		t.Fatalf("expected directory error, got %q", text)
	}
}

func TestReadFile_PathTraversal(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "../../../etc/passwd"})
	text := responseText(resp)
	if !strings.Contains(text, "escapes workspace root") {
		t.Fatalf("expected path escape error, got %q", text)
	}
}

func TestReadFile_LongLineTruncation(t *testing.T) {
	root := tmpWorkspace(t)
	longLine := strings.Repeat("a", 3000)
	writeTestFile(t, root, "long.txt", longLine)

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "read_file", map[string]any{"path": "long.txt"})
	text := responseText(resp)
	if !strings.Contains(text, "truncated") {
		t.Fatalf("expected truncation marker for long line")
	}
}

// =====================================================================
// write_file
// =====================================================================

func TestWriteFile_Basic(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "write_file", map[string]any{
		"path":    "output.txt",
		"content": "hello world",
	})
	text := responseText(resp)
	if !strings.Contains(text, "wrote") {
		t.Fatalf("expected write confirmation, got %q", text)
	}

	data, err := os.ReadFile(filepath.Join(root, "output.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("content mismatch: %q", string(data))
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "existing.txt", "old content")

	backend := NewLocalBackend(root)
	execTool(t, backend, "write_file", map[string]any{
		"path":    "existing.txt",
		"content": "new content",
	})

	data, err := os.ReadFile(filepath.Join(root, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Fatalf("overwrite failed: %q", string(data))
	}
}

func TestWriteFile_CreateDirs(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "write_file", map[string]any{
		"path":        "a/b/c/file.txt",
		"content":     "deep",
		"create_dirs": true,
	})
	text := responseText(resp)
	if !strings.Contains(text, "wrote") {
		t.Fatalf("expected write confirmation, got %q", text)
	}

	data, err := os.ReadFile(filepath.Join(root, "a", "b", "c", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Fatalf("content mismatch: %q", string(data))
	}
}

func TestWriteFile_NoDirCreation(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "write_file", map[string]any{
		"path":    "missing/dir/file.txt",
		"content": "fail",
	})
	text := responseText(resp)
	if !strings.Contains(text, "directory does not exist") {
		t.Fatalf("expected dir error, got %q", text)
	}
}

func TestWriteFile_MissingPath(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "write_file", map[string]any{"content": "no path"})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestWriteFile_PathTraversal(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "write_file", map[string]any{
		"path":    "../escape.txt",
		"content": "bad",
	})
	text := responseText(resp)
	if !strings.Contains(text, "escapes workspace root") {
		t.Fatalf("expected escape error, got %q", text)
	}
}

func TestWriteFile_AtomicNoPartialWrite(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "atomic.txt", "original content")

	backend := NewLocalBackend(root)
	execTool(t, backend, "write_file", map[string]any{
		"path":    "atomic.txt",
		"content": "new content that replaces everything",
	})

	data, err := os.ReadFile(filepath.Join(root, "atomic.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content that replaces everything" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

// =====================================================================
// edit_file
// =====================================================================

func TestEditFile_SingleReplace(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "code.go", "func hello() {\n\treturn \"hi\"\n}\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":       "code.go",
		"old_string": "return \"hi\"",
		"new_string": "return \"hello\"",
	})
	text := responseText(resp)
	if !strings.Contains(text, "1 replacement") {
		t.Fatalf("expected 1 replacement, got %q", text)
	}

	data, err := os.ReadFile(filepath.Join(root, "code.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "return \"hello\"") {
		t.Fatalf("edit not applied: %q", string(data))
	}
}

func TestEditFile_ReplaceAll(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "dup.txt", "foo bar foo baz foo")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":        "dup.txt",
		"old_string":  "foo",
		"new_string":  "qux",
		"replace_all": true,
	})
	text := responseText(resp)
	if !strings.Contains(text, "3 replacement") {
		t.Fatalf("expected 3 replacements, got %q", text)
	}

	data, err := os.ReadFile(filepath.Join(root, "dup.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "qux bar qux baz qux" {
		t.Fatalf("replace_all failed: %q", string(data))
	}
}

func TestEditFile_AmbiguousReject(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "ambig.txt", "foo bar foo")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":       "ambig.txt",
		"old_string": "foo",
		"new_string": "baz",
	})
	text := responseText(resp)
	if !strings.Contains(text, "found 2 times") {
		t.Fatalf("expected ambiguous error, got %q", text)
	}

	// Verify file unchanged
	data, err := os.ReadFile(filepath.Join(root, "ambig.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "foo bar foo" {
		t.Fatalf("file modified despite ambiguity: %q", string(data))
	}
}

func TestEditFile_NotFound(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "nope.txt", "hello world")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":       "nope.txt",
		"old_string": "missing text",
		"new_string": "replacement",
	})
	text := responseText(resp)
	if !strings.Contains(text, "old_string not found") {
		t.Fatalf("expected not-found error, got %q", text)
	}
}

func TestEditFile_MismatchDiagnostics(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "diag.txt", "func main() {\n\tfmt.Println(\"hello\")\n}\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":       "diag.txt",
		"old_string": "fmt.Println(\"world\")",
		"new_string": "fmt.Println(\"changed\")",
	})
	text := responseText(resp)
	if !strings.Contains(text, "old_string not found") {
		t.Fatalf("expected not-found error, got %q", text)
	}
	if !strings.Contains(text, "fmt.Println") {
		t.Fatalf("expected diagnostic context, got %q", text)
	}
}

func TestEditFile_MissingParams(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "edit_file", map[string]any{"path": "x.txt"})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestEditFile_FileNotExists(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "edit_file", map[string]any{
		"path":       "nonexistent.txt",
		"old_string": "a",
		"new_string": "b",
	})
	text := responseText(resp)
	if !strings.Contains(text, "file not found") {
		t.Fatalf("expected file not found, got %q", text)
	}
}

func TestEditFile_PreservesUnchangedContent(t *testing.T) {
	root := tmpWorkspace(t)
	content := "line1\nline2\nTARGET\nline4\nline5\n"
	writeTestFile(t, root, "preserve.txt", content)

	backend := NewLocalBackend(root)
	execTool(t, backend, "edit_file", map[string]any{
		"path":       "preserve.txt",
		"old_string": "TARGET",
		"new_string": "REPLACED",
	})

	data, err := os.ReadFile(filepath.Join(root, "preserve.txt"))
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nline2\nREPLACED\nline4\nline5\n"
	if string(data) != expected {
		t.Fatalf("unexpected content:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

// =====================================================================
// LocalBackend
// =====================================================================

func TestLocalBackend_UnknownTool(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	_, err := backend.ExecuteTool(context.Background(), ToolRequest{
		ToolName: "unknown_tool",
		Params:   map[string]any{},
	}, nil)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =====================================================================
// Factory
// =====================================================================

func TestNewLocalTools_ReturnsAllFileTools(t *testing.T) {
	root := tmpWorkspace(t)
	tools := NewLocalTools(root, LocalToolsOptions{})

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{"read_file", "write_file", "edit_file"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestNewLocalTools_ToolsExecute(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "factory_test.txt", "factory content")

	tools := NewLocalTools(root, LocalToolsOptions{})

	for _, tool := range tools {
		if tool.Name == "read_file" {
			result, err := tool.Execute(context.Background(), "call_1", map[string]any{"path": "factory_test.txt"}, nil)
			if err != nil {
				t.Fatalf("execute error: %v", err)
			}
			if len(result.Content) == 0 {
				t.Fatal("empty result")
			}
			tc, ok := result.Content[0].(*ai.TextContent)
			if !ok {
				t.Fatalf("expected TextContent, got %T", result.Content[0])
			}
			if !strings.Contains(tc.Text, "factory content") {
				t.Fatalf("expected factory content, got %q", tc.Text)
			}
			return
		}
	}
	t.Fatal("read_file tool not found in factory output")
}
