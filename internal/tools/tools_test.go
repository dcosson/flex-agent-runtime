package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

func TestNewLocalTools_ReturnsAllTools(t *testing.T) {
	root := tmpWorkspace(t)
	tools := NewLocalTools(root, LocalToolsOptions{})

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name] = true
	}

	expected := []string{
		"read_file", "write_file", "edit_file",
		"grep", "glob", "bash",
		"git_status", "git_diff", "git_log", "git_show",
		"git_add", "git_commit",
	}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

// =====================================================================
// grep
// =====================================================================

func TestGrep_BasicRegex(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "code.go", "func main() {\n\tfmt.Println(\"hello\")\n}\n")
	writeTestFile(t, root, "other.txt", "no match here\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{"pattern": "fmt\\.Println"})
	text := responseText(resp)
	// Verify file:line:col format (col=2 because of leading tab)
	if !strings.Contains(text, "code.go:2:2:") {
		t.Fatalf("expected match at code.go:2:2, got %q", text)
	}
}

func TestGrep_LiteralSearch(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "data.txt", "foo.bar\nfoo_bar\nfooXbar\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{
		"pattern": "foo.bar",
		"literal": true,
	})
	text := responseText(resp)
	if !strings.Contains(text, "Found 1 match") {
		t.Fatalf("expected 1 literal match, got %q", text)
	}
}

func TestGrep_IncludeFilter(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "main.go", "package main\n")
	writeTestFile(t, root, "readme.md", "package info\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{
		"pattern": "package",
		"include": "*.go",
	})
	text := responseText(resp)
	if !strings.Contains(text, "main.go") {
		t.Fatalf("expected main.go match, got %q", text)
	}
	if strings.Contains(text, "readme.md") {
		t.Fatalf("readme.md should be excluded by include filter")
	}
}

func TestGrep_ExcludeFilter(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "main.go", "package main\n")
	writeTestFile(t, root, "test.go", "package main\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{
		"pattern": "package",
		"exclude": "test.go",
	})
	text := responseText(resp)
	if !strings.Contains(text, "main.go") {
		t.Fatalf("expected main.go match, got %q", text)
	}
	if strings.Contains(text, "test.go") {
		t.Fatalf("test.go should be excluded")
	}
}

func TestGrep_NoMatches(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "empty.txt", "nothing here\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{"pattern": "zzzzzzz"})
	text := responseText(resp)
	if !strings.Contains(text, "no matches found") {
		t.Fatalf("expected no matches, got %q", text)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{"pattern": "[invalid"})
	text := responseText(resp)
	if !strings.Contains(text, "invalid regex") {
		t.Fatalf("expected regex error, got %q", text)
	}
}

func TestGrep_MissingPattern(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestGrep_MaxResults(t *testing.T) {
	root := tmpWorkspace(t)
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "match_line")
	}
	writeTestFile(t, root, "many.txt", strings.Join(lines, "\n"))

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{
		"pattern":     "match_line",
		"max_results": float64(5),
	})
	text := responseText(resp)
	if !strings.Contains(text, "truncated at 5") {
		t.Fatalf("expected truncation, got %q", text)
	}
}

func TestGrep_SkipsHiddenDirs(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "visible.txt", "findme\n")
	writeTestFile(t, root, ".hidden/secret.txt", "findme\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "grep", map[string]any{"pattern": "findme"})
	text := responseText(resp)
	if !strings.Contains(text, "visible.txt") {
		t.Fatalf("expected visible.txt match, got %q", text)
	}
	if strings.Contains(text, ".hidden") {
		t.Fatalf("should skip hidden directories")
	}
}

// =====================================================================
// glob
// =====================================================================

func TestGlob_BasicPattern(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "main.go", "x")
	writeTestFile(t, root, "test.go", "x")
	writeTestFile(t, root, "readme.md", "x")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{"pattern": "*.go"})
	text := responseText(resp)
	if !strings.Contains(text, "main.go") || !strings.Contains(text, "test.go") {
		t.Fatalf("expected .go files, got %q", text)
	}
	if strings.Contains(text, "readme.md") {
		t.Fatalf("should not match .md files")
	}
}

func TestGlob_RecursivePattern(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "a/b/c.go", "x")
	writeTestFile(t, root, "a/d.go", "x")
	writeTestFile(t, root, "e.txt", "x")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{"pattern": "**/*.go"})
	text := responseText(resp)
	if !strings.Contains(text, "c.go") || !strings.Contains(text, "d.go") {
		t.Fatalf("expected recursive .go matches, got %q", text)
	}
	if strings.Contains(text, "e.txt") {
		t.Fatalf("should not match .txt")
	}
}

func TestGlob_NoMatches(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "file.txt", "x")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{"pattern": "*.xyz"})
	text := responseText(resp)
	if !strings.Contains(text, "no files matched") {
		t.Fatalf("expected no matches, got %q", text)
	}
}

func TestGlob_SortedOutput(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "z.txt", "x")
	writeTestFile(t, root, "a.txt", "x")
	writeTestFile(t, root, "m.txt", "x")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{"pattern": "*.txt"})
	text := responseText(resp)
	aIdx := strings.Index(text, "a.txt")
	mIdx := strings.Index(text, "m.txt")
	zIdx := strings.Index(text, "z.txt")
	if aIdx > mIdx || mIdx > zIdx {
		t.Fatalf("expected sorted output, got %q", text)
	}
}

func TestGlob_MaxResults(t *testing.T) {
	root := tmpWorkspace(t)
	for i := 0; i < 10; i++ {
		writeTestFile(t, root, fmt.Sprintf("file%d.txt", i), "x")
	}

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{
		"pattern":     "*.txt",
		"max_results": float64(3),
	})
	text := responseText(resp)
	if !strings.Contains(text, "truncated at 3") {
		t.Fatalf("expected truncation, got %q", text)
	}
}

func TestGlob_MissingPattern(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestGlob_SkipsHiddenDirs(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "visible.txt", "x")
	writeTestFile(t, root, ".hidden/secret.txt", "x")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "glob", map[string]any{"pattern": "**/*.txt"})
	text := responseText(resp)
	if !strings.Contains(text, "visible.txt") {
		t.Fatalf("expected visible.txt, got %q", text)
	}
	if strings.Contains(text, ".hidden") {
		t.Fatalf("should skip hidden directories")
	}
}

// =====================================================================
// bash
// =====================================================================

func TestBash_BasicCommand(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "bash", map[string]any{"cmd": "echo hello world"})
	text := responseText(resp)
	if !strings.Contains(text, "hello world") {
		t.Fatalf("expected 'hello world', got %q", text)
	}
	if !strings.Contains(text, "Exit code: 0") {
		t.Fatalf("expected exit code 0, got %q", text)
	}
}

func TestBash_ExitCode(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "bash", map[string]any{"cmd": "exit 42"})
	text := responseText(resp)
	if !strings.Contains(text, "Exit code: 42") {
		t.Fatalf("expected exit code 42, got %q", text)
	}
	if resp.ExitCode == nil || *resp.ExitCode != 42 {
		t.Fatalf("expected ExitCode=42 in response")
	}
}

func TestBash_Stderr(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "bash", map[string]any{"cmd": "echo error >&2"})
	text := responseText(resp)
	if !strings.Contains(text, "STDERR:") {
		t.Fatalf("expected STDERR label, got %q", text)
	}
	if !strings.Contains(text, "error") {
		t.Fatalf("expected error in stderr, got %q", text)
	}
}

func TestBash_WorkingDirectory(t *testing.T) {
	root := tmpWorkspace(t)
	writeTestFile(t, root, "marker.txt", "found-it")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "bash", map[string]any{"cmd": "cat marker.txt"})
	text := responseText(resp)
	if !strings.Contains(text, "found-it") {
		t.Fatalf("expected 'found-it', got %q", text)
	}
}

func TestBash_MissingCmd(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "bash", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestBash_Timeout(t *testing.T) {
	root := tmpWorkspace(t)
	backend := NewLocalBackend(root)

	resp := execTool(t, backend, "bash", map[string]any{
		"cmd":        "sleep 10",
		"timeout_ms": float64(100),
	})
	text := responseText(resp)
	if !strings.Contains(text, "timed out") {
		t.Fatalf("expected timeout message, got %q", text)
	}
}

// =====================================================================
// git tools
// =====================================================================

func initGitRepo(t *testing.T) string {
	t.Helper()
	root := tmpWorkspace(t)
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init failed: %v\n%s", err, out)
		}
	}
	return root
}

func TestGitStatus_Clean(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "initial.txt", "hello")
	gitExec(t, root, "add", ".")
	gitExec(t, root, "commit", "-m", "initial")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_status", map[string]any{})
	text := responseText(resp)
	// Clean repo should have no porcelain output
	if strings.Contains(text, "??") || strings.Contains(text, " M ") {
		t.Fatalf("expected clean status, got %q", text)
	}
}

func TestGitStatus_ModifiedFile(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "file.txt", "original")
	gitExec(t, root, "add", ".")
	gitExec(t, root, "commit", "-m", "initial")
	writeTestFile(t, root, "file.txt", "modified")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_status", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "file.txt") {
		t.Fatalf("expected modified file.txt in status, got %q", text)
	}
}

func TestGitDiff_ShowChanges(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "diff.txt", "line1\nline2\n")
	gitExec(t, root, "add", ".")
	gitExec(t, root, "commit", "-m", "initial")
	writeTestFile(t, root, "diff.txt", "line1\nline2\nline3\n")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_diff", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "+line3") {
		t.Fatalf("expected +line3 in diff, got %q", text)
	}
}

func TestGitLog_ShowCommits(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "log.txt", "content")
	gitExec(t, root, "add", ".")
	gitExec(t, root, "commit", "-m", "test commit message")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_log", map[string]any{"n": float64(5)})
	text := responseText(resp)
	if !strings.Contains(text, "test commit message") {
		t.Fatalf("expected commit message in log, got %q", text)
	}
}

func TestGitShow_HeadCommit(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "show.txt", "show content")
	gitExec(t, root, "add", ".")
	gitExec(t, root, "commit", "-m", "show test")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_show", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "show test") {
		t.Fatalf("expected commit info in show, got %q", text)
	}
}

func TestGitAdd_StageFile(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "staged.txt", "to stage")

	backend := NewLocalBackend(root)
	execTool(t, backend, "git_add", map[string]any{
		"paths": []any{"staged.txt"},
	})

	// Verify file is staged
	resp := execTool(t, backend, "git_status", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "A") || !strings.Contains(text, "staged.txt") {
		t.Fatalf("expected staged file in status, got %q", text)
	}
}

func TestGitAdd_MissingPaths(t *testing.T) {
	root := initGitRepo(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_add", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

func TestGitCommit_CreateCommit(t *testing.T) {
	root := initGitRepo(t)
	writeTestFile(t, root, "commit.txt", "commit content")
	gitExec(t, root, "add", ".")

	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_commit", map[string]any{
		"message": "automated test commit",
	})
	text := responseText(resp)
	if !strings.Contains(text, "automated test commit") {
		t.Fatalf("expected commit message confirmation, got %q", text)
	}
}

func TestGitCommit_MissingMessage(t *testing.T) {
	root := initGitRepo(t)
	backend := NewLocalBackend(root)
	resp := execTool(t, backend, "git_commit", map[string]any{})
	text := responseText(resp)
	if !strings.Contains(text, "missing required parameter") {
		t.Fatalf("expected missing param error, got %q", text)
	}
}

// =====================================================================
// SandboxBackend
// =====================================================================

type fakeSandboxClient struct {
	lastReq    ToolRequest
	lastSessID string
	response   *ToolResponse
	err        error
}

func (f *fakeSandboxClient) ExecuteTool(_ context.Context, sessionID string, req ToolRequest, _ func(ToolProgress)) (*ToolResponse, error) {
	f.lastReq = req
	f.lastSessID = sessionID
	return f.response, f.err
}

func TestSandboxBackend_ForwardsRequest(t *testing.T) {
	client := &fakeSandboxClient{
		response: textResponse("sandbox result"),
	}
	backend := NewSandboxBackend(client, "sess-123")

	resp, err := backend.ExecuteTool(context.Background(), ToolRequest{
		ToolName:   "read_file",
		ToolCallID: "tc-1",
		Params:     map[string]any{"path": "test.txt"},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.lastSessID != "sess-123" {
		t.Fatalf("expected session ID 'sess-123', got %q", client.lastSessID)
	}
	if client.lastReq.ToolName != "read_file" {
		t.Fatalf("expected tool name 'read_file', got %q", client.lastReq.ToolName)
	}
	if client.lastReq.SessionID != "sess-123" {
		t.Fatalf("expected SessionID propagated, got %q", client.lastReq.SessionID)
	}

	text := responseText(resp)
	if !strings.Contains(text, "sandbox result") {
		t.Fatalf("expected sandbox result, got %q", text)
	}
}

func TestSandboxBackend_NilClient(t *testing.T) {
	backend := NewSandboxBackend(nil, "sess-1")
	_, err := backend.ExecuteTool(context.Background(), ToolRequest{
		ToolName: "test",
	}, nil)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewSandboxTools_ReturnsSameToolNames(t *testing.T) {
	client := &fakeSandboxClient{
		response: textResponse("ok"),
	}
	sandboxTools := NewSandboxTools(client, "sess-1")
	localTools := NewLocalTools(tmpWorkspace(t), LocalToolsOptions{})

	localNames := make(map[string]bool)
	for _, tool := range localTools {
		localNames[tool.Name] = true
	}

	sandboxNames := make(map[string]bool)
	for _, tool := range sandboxTools {
		sandboxNames[tool.Name] = true
	}

	if len(localNames) != len(sandboxNames) {
		t.Fatalf("tool count mismatch: local=%d sandbox=%d", len(localNames), len(sandboxNames))
	}
	for name := range localNames {
		if !sandboxNames[name] {
			t.Errorf("sandbox missing tool: %s", name)
		}
	}
}

// helper for git tests
func gitExec(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
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
