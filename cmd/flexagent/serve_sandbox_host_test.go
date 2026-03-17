package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseSandboxHostConfigDefaults(t *testing.T) {
	cfg := parseSandboxHostConfig(nil)
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.StorageBackend != "zfs" {
		t.Fatalf("StorageBackend = %q, want zfs", cfg.StorageBackend)
	}
	if cfg.ContainerRuntime != "gvisor" {
		t.Fatalf("ContainerRuntime = %q, want gvisor", cfg.ContainerRuntime)
	}
	if cfg.ToolTimeout != 5*time.Minute {
		t.Fatalf("ToolTimeout = %s, want 5m", cfg.ToolTimeout)
	}
	if cfg.RPCMaxMessageBytes != 16<<20 {
		t.Fatalf("RPCMaxMessageBytes = %d, want %d", cfg.RPCMaxMessageBytes, 16<<20)
	}
	if cfg.EnableTerminal {
		t.Fatal("EnableTerminal should default to false")
	}
}

func TestParseSandboxHostConfigFlags(t *testing.T) {
	args := []string{
		"-listen", ":9090",
		"-storage-backend", "local-disk",
		"-container-runtime", "none",
		"-sessions-root-dir", "/tmp/sessions",
		"-max-sessions", "10",
		"-tool-timeout", "1m",
		"-auth-token", "abc",
		"-enable-terminal",
	}
	cfg := parseSandboxHostConfig(args)
	if cfg.ListenAddr != ":9090" {
		t.Fatalf("ListenAddr = %q, want :9090", cfg.ListenAddr)
	}
	if cfg.StorageBackend != "local-disk" {
		t.Fatalf("StorageBackend = %q, want local-disk", cfg.StorageBackend)
	}
	if cfg.ContainerRuntime != "none" {
		t.Fatalf("ContainerRuntime = %q, want none", cfg.ContainerRuntime)
	}
	if cfg.SessionsRootDir != "/tmp/sessions" {
		t.Fatalf("SessionsRootDir = %q, want /tmp/sessions", cfg.SessionsRootDir)
	}
	if cfg.MaxSessions != 10 {
		t.Fatalf("MaxSessions = %d, want 10", cfg.MaxSessions)
	}
	if cfg.ToolTimeout != time.Minute {
		t.Fatalf("ToolTimeout = %s, want 1m", cfg.ToolTimeout)
	}
	if cfg.AuthToken != "abc" {
		t.Fatalf("AuthToken = %q, want abc", cfg.AuthToken)
	}
	if !cfg.EnableTerminal {
		t.Fatal("EnableTerminal should be true")
	}
}

func TestSandboxHostConfigValidateZFS(t *testing.T) {
	cfg := sandboxHostConfig{
		StorageBackend:     "zfs",
		ContainerRuntime:   "gvisor",
		PoolName:           "testpool",
		BasesDataset:       "testpool/bases",
		SessionsDataset:    "testpool/sessions",
		BundleBaseDir:      "/var/lib/sandbox/bundles",
		RPCMaxMessageBytes: 1024,
		APIVersion:         "v1",
		MinAPIVersion:      "v1",
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
}

func TestSandboxHostConfigValidateLocalDisk(t *testing.T) {
	cfg := sandboxHostConfig{
		StorageBackend:     "local-disk",
		ContainerRuntime:   "none",
		SessionsRootDir:    "/tmp/sessions",
		RPCMaxMessageBytes: 1024,
		APIVersion:         "v1",
		MinAPIVersion:      "v1",
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
}

func TestSandboxHostConfigValidateMissingRequired(t *testing.T) {
	cfg := sandboxHostConfig{
		StorageBackend:   "zfs",
		ContainerRuntime: "gvisor",
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error")
	}
	checks := []string{
		"pool is required for zfs backend",
		"bases-dataset is required for zfs backend",
		"sessions-dataset is required for zfs backend",
		"bundle-base-dir is required for gvisor runtime",
		"rpc-max-message-bytes must be > 0",
		"api-version is required",
		"min-api-version is required",
	}
	for _, want := range checks {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validate() error missing %q: %v", want, err)
		}
	}
}

func TestSandboxHostConfigValidateInvalidBackend(t *testing.T) {
	cfg := sandboxHostConfig{
		StorageBackend:     "nosuch",
		ContainerRuntime:   "none",
		RPCMaxMessageBytes: 1024,
		APIVersion:         "v1",
		MinAPIVersion:      "v1",
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error")
	}
	if !strings.Contains(err.Error(), "invalid storage-backend") {
		t.Fatalf("expected storage-backend error, got: %v", err)
	}
}

func TestSandboxHostConfigValidateInvalidRuntime(t *testing.T) {
	cfg := sandboxHostConfig{
		StorageBackend:     "local-disk",
		ContainerRuntime:   "nosuch",
		SessionsRootDir:    "/tmp",
		RPCMaxMessageBytes: 1024,
		APIVersion:         "v1",
		MinAPIVersion:      "v1",
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error")
	}
	if !strings.Contains(err.Error(), "invalid container-runtime") {
		t.Fatalf("expected container-runtime error, got: %v", err)
	}
}
