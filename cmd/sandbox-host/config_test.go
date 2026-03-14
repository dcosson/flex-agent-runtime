package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvParsers(t *testing.T) {
	t.Setenv("SANDBOX_HOST_MAX_SESSIONS", "42")
	t.Setenv("SANDBOX_HOST_DEFAULT_QUOTA", "1234")
	t.Setenv("SANDBOX_HOST_TOOL_TIMEOUT", "30s")
	t.Setenv("SANDBOX_HOST_SUDO", "true")
	t.Setenv("SANDBOX_HOST_LISTEN", ":9191")

	if got := envIntOrDefault("SANDBOX_HOST_MAX_SESSIONS", 0); got != 42 {
		t.Fatalf("envIntOrDefault = %d, want 42", got)
	}
	if got := envInt64OrDefault("SANDBOX_HOST_DEFAULT_QUOTA", 0); got != 1234 {
		t.Fatalf("envInt64OrDefault = %d, want 1234", got)
	}
	if got := envDurationOrDefault("SANDBOX_HOST_TOOL_TIMEOUT", 0); got != 30*time.Second {
		t.Fatalf("envDurationOrDefault = %s, want 30s", got)
	}
	if got := envBoolOrDefault("SANDBOX_HOST_SUDO", false); !got {
		t.Fatalf("envBoolOrDefault = false, want true")
	}
	if got := envOrDefault("SANDBOX_HOST_LISTEN", ":8080"); got != ":9191" {
		t.Fatalf("envOrDefault = %q, want :9191", got)
	}
}

func TestEnvParsersFallback(t *testing.T) {
	const missing = "SANDBOX_HOST_MISSING_ENV_FOR_TEST"
	_ = os.Unsetenv(missing)
	if got := envIntOrDefault(missing, 7); got != 7 {
		t.Fatalf("fallback int = %d, want 7", got)
	}
	if got := envInt64OrDefault(missing, 9); got != 9 {
		t.Fatalf("fallback int64 = %d, want 9", got)
	}
	if got := envDurationOrDefault(missing, time.Minute); got != time.Minute {
		t.Fatalf("fallback duration = %s, want %s", got, time.Minute)
	}
	if got := envBoolOrDefault(missing, true); !got {
		t.Fatalf("fallback bool = false, want true")
	}
	if got := envOrDefault(missing, "x"); got != "x" {
		t.Fatalf("fallback string = %q, want x", got)
	}
}

func TestApplyConfigFile(t *testing.T) {
	cfg := Config{
		ListenAddr:      ":8080",
		PoolName:        "tank",
		BasesDataset:    "tank/bases",
		SessionsDataset: "tank/sessions",
	}
	p := filepath.Join(t.TempDir(), "sandbox-host.json")
	if err := os.WriteFile(p, []byte(`{
  "listen": ":9090",
  "pool_name": "poolA",
  "bases_dataset": "poolA/bases",
  "sessions_dataset": "poolA/sessions",
  "max_sessions": 77,
  "default_quota": 9999,
  "tool_timeout": "45s",
  "sudo": true,
  "rpc_max_message_bytes": 12345,
  "api_version": "v7",
  "min_api_version": "v3"
}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := applyConfigFile(&cfg, p); err != nil {
		t.Fatalf("applyConfigFile error: %v", err)
	}
	if cfg.ListenAddr != ":9090" || cfg.PoolName != "poolA" || cfg.BasesDataset != "poolA/bases" || cfg.SessionsDataset != "poolA/sessions" {
		t.Fatalf("basic fields not applied: %+v", cfg)
	}
	if cfg.MaxSessions != 77 || cfg.DefaultQuota != 9999 || cfg.ToolTimeout != 45*time.Second {
		t.Fatalf("numeric/duration fields not applied: %+v", cfg)
	}
	if !cfg.UseSudo || cfg.RPCMaxMessageBytes != 12345 || cfg.APIVersion != "v7" || cfg.MinAPIVersion != "v3" {
		t.Fatalf("remaining fields not applied: %+v", cfg)
	}
}
