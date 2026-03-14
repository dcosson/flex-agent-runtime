package termmux

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDirManager_StablePath(t *testing.T) {
	m := NewConfigDirManager("/tmp/test-config")

	path := m.StablePath("session-123")
	if path != "/tmp/test-config/session-123" {
		t.Errorf("unexpected path: %s", path)
	}

	// Same session ID → same path (deterministic)
	path2 := m.StablePath("session-123")
	if path != path2 {
		t.Error("StablePath is not deterministic")
	}
}

func TestConfigDirManager_EnsureDir(t *testing.T) {
	dir := t.TempDir()
	m := NewConfigDirManager(dir)

	path, err := m.EnsureDir("s1")
	if err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("expected 0700 permissions, got %o", info.Mode().Perm())
	}

	// Calling again should be idempotent
	path2, err := m.EnsureDir("s1")
	if err != nil {
		t.Fatalf("EnsureDir (2nd): %v", err)
	}
	if path != path2 {
		t.Error("EnsureDir returned different path on second call")
	}
}

func TestConfigDirManager_Cleanup(t *testing.T) {
	dir := t.TempDir()
	m := NewConfigDirManager(dir)

	_, err := m.EnsureDir("s1")
	if err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Write a file inside
	if err := m.InjectFile("s1", "test.txt", []byte("hello")); err != nil {
		t.Fatalf("InjectFile: %v", err)
	}

	if err := m.Cleanup("s1"); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	// Directory should be gone
	if _, err := os.Stat(m.StablePath("s1")); !os.IsNotExist(err) {
		t.Error("expected directory to be removed after cleanup")
	}

	// Cleanup of non-existent dir should not error
	if err := m.Cleanup("nonexistent"); err != nil {
		t.Errorf("Cleanup of nonexistent: %v", err)
	}
}

func TestConfigDirManager_InjectFile(t *testing.T) {
	dir := t.TempDir()
	m := NewConfigDirManager(dir)

	_, err := m.EnsureDir("s1")
	if err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Inject a file
	if err := m.InjectFile("s1", "CLAUDE.md", []byte("# Instructions")); err != nil {
		t.Fatalf("InjectFile: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(m.StablePath("s1"), "CLAUDE.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "# Instructions" {
		t.Errorf("unexpected content: %s", string(data))
	}
}

func TestConfigDirManager_InjectFileNestedPath(t *testing.T) {
	dir := t.TempDir()
	m := NewConfigDirManager(dir)

	_, err := m.EnsureDir("s1")
	if err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	// Inject a file in a nested path
	if err := m.InjectFile("s1", "mcp/servers.json", []byte("{}")); err != nil {
		t.Fatalf("InjectFile nested: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(m.StablePath("s1"), "mcp", "servers.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("unexpected content: %s", string(data))
	}
}
