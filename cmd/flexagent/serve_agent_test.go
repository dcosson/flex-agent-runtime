package main

import (
	"testing"
	"time"
)

func TestParseAgentConfigDefaults(t *testing.T) {
	cfg := parseAgentConfig(nil)
	if cfg.ListenAddr != ":8081" {
		t.Fatalf("ListenAddr = %q, want :8081", cfg.ListenAddr)
	}
	if cfg.MaxSessions != 0 {
		t.Fatalf("MaxSessions = %d, want 0", cfg.MaxSessions)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.RPCMaxMessageBytes != 16<<20 {
		t.Fatalf("RPCMaxMessageBytes = %d, want %d", cfg.RPCMaxMessageBytes, 16<<20)
	}
	if cfg.APIVersion != "v1" {
		t.Fatalf("APIVersion = %q, want v1", cfg.APIVersion)
	}
	if cfg.MinAPIVersion != "v1" {
		t.Fatalf("MinAPIVersion = %q, want v1", cfg.MinAPIVersion)
	}
	if cfg.AuthToken != "" {
		t.Fatalf("AuthToken = %q, want empty", cfg.AuthToken)
	}
}

func TestParseAgentConfigFlags(t *testing.T) {
	args := []string{
		"-listen", ":9090",
		"-max-sessions", "5",
		"-shutdown-timeout", "10s",
		"-auth-token", "secret",
		"-api-version", "v2",
		"-min-api-version", "v1",
	}
	cfg := parseAgentConfig(args)
	if cfg.ListenAddr != ":9090" {
		t.Fatalf("ListenAddr = %q, want :9090", cfg.ListenAddr)
	}
	if cfg.MaxSessions != 5 {
		t.Fatalf("MaxSessions = %d, want 5", cfg.MaxSessions)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.AuthToken != "secret" {
		t.Fatalf("AuthToken = %q, want secret", cfg.AuthToken)
	}
	if cfg.APIVersion != "v2" {
		t.Fatalf("APIVersion = %q, want v2", cfg.APIVersion)
	}
}

func TestParseAgentConfigFromEnv(t *testing.T) {
	t.Setenv("FLEXAGENT_LISTEN", ":7777")
	t.Setenv("FLEXAGENT_MAX_SESSIONS", "10")
	t.Setenv("FLEXAGENT_SHUTDOWN_TIMEOUT", "1m")
	t.Setenv("FLEXAGENT_AUTH_TOKEN", "envtoken")

	cfg := parseAgentConfig(nil)
	if cfg.ListenAddr != ":7777" {
		t.Fatalf("ListenAddr = %q, want :7777", cfg.ListenAddr)
	}
	if cfg.MaxSessions != 10 {
		t.Fatalf("MaxSessions = %d, want 10", cfg.MaxSessions)
	}
	if cfg.ShutdownTimeout != time.Minute {
		t.Fatalf("ShutdownTimeout = %s, want 1m", cfg.ShutdownTimeout)
	}
	if cfg.AuthToken != "envtoken" {
		t.Fatalf("AuthToken = %q, want envtoken", cfg.AuthToken)
	}
}

func TestMakeAuthHookNilWhenEmpty(t *testing.T) {
	hook := makeAuthHook("")
	if hook != nil {
		t.Fatal("expected nil auth hook for empty token")
	}
}

func TestMakeAuthHookRejectsInvalid(t *testing.T) {
	hook := makeAuthHook("secret")
	if hook == nil {
		t.Fatal("expected non-nil auth hook")
	}

	headers := make(map[string][]string)
	headers["Authorization"] = []string{"Bearer wrong"}
	err := hook(nil, "/test", headers)
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestMakeAuthHookAcceptsValid(t *testing.T) {
	hook := makeAuthHook("secret")

	headers := make(map[string][]string)
	headers["Authorization"] = []string{"Bearer secret"}
	err := hook(nil, "/test", headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
