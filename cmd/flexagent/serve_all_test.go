package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseServeAllConfigDefaults(t *testing.T) {
	cfg := parseServeAllConfig(nil)
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.AgentMaxSessions != 0 {
		t.Fatalf("AgentMaxSessions = %d, want 0", cfg.AgentMaxSessions)
	}
	if cfg.StorageBackend != "zfs" {
		t.Fatalf("StorageBackend = %q, want zfs", cfg.StorageBackend)
	}
	if cfg.ContainerRuntime != "gvisor" {
		t.Fatalf("ContainerRuntime = %q, want gvisor", cfg.ContainerRuntime)
	}
}

func TestParseServeAllConfigFlags(t *testing.T) {
	args := []string{
		"-listen", ":7070",
		"-shutdown-timeout", "15s",
		"-agent-max-sessions", "3",
		"-storage-backend", "local-disk",
		"-container-runtime", "none",
		"-sessions-root-dir", "/tmp/sessions",
	}
	cfg := parseServeAllConfig(args)
	if cfg.ListenAddr != ":7070" {
		t.Fatalf("ListenAddr = %q, want :7070", cfg.ListenAddr)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 15s", cfg.ShutdownTimeout)
	}
	if cfg.AgentMaxSessions != 3 {
		t.Fatalf("AgentMaxSessions = %d, want 3", cfg.AgentMaxSessions)
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
}

func TestServeAllConfigValidateValid(t *testing.T) {
	cfg := serveAllConfig{
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

func TestServeAllConfigValidateMissing(t *testing.T) {
	cfg := serveAllConfig{
		StorageBackend:   "zfs",
		ContainerRuntime: "gvisor",
	}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error")
	}
	checks := []string{
		"pool is required",
		"bases-dataset is required",
		"sessions-dataset is required",
		"bundle-base-dir is required",
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
