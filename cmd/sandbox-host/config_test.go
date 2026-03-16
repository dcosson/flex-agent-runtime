package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestEnvParsers(t *testing.T) {
	t.Setenv("SANDBOX_MAX_SESSIONS", "42")
	t.Setenv("SANDBOX_DEFAULT_QUOTA", "1234")
	t.Setenv("SANDBOX_TOOL_TIMEOUT", "30s")
	t.Setenv("SANDBOX_SUDO", "true")
	t.Setenv("SANDBOX_HOST_LISTEN", ":9191")

	if got := envIntOrDefault("SANDBOX_MAX_SESSIONS", 0); got != 42 {
		t.Fatalf("envIntOrDefault = %d, want 42", got)
	}
	if got := envInt64OrDefault("SANDBOX_DEFAULT_QUOTA", 0); got != 1234 {
		t.Fatalf("envInt64OrDefault = %d, want 1234", got)
	}
	if got := envDurationOrDefault("SANDBOX_TOOL_TIMEOUT", 0); got != 30*time.Second {
		t.Fatalf("envDurationOrDefault = %s, want 30s", got)
	}
	if got := envBoolOrDefault("SANDBOX_SUDO", false); !got {
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

func TestConfigValidate(t *testing.T) {
	cfg := Config{
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
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

func TestConfigValidateMissingRequired(t *testing.T) {
	cfg := Config{
		StorageBackend:   "zfs",
		ContainerRuntime: "gvisor",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error")
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
			t.Fatalf("Validate() error missing %q: %v", want, err)
		}
	}
}

func TestConfigValidateLocalDiskNone(t *testing.T) {
	cfg := Config{
		StorageBackend:     "local-disk",
		ContainerRuntime:   "none",
		SessionsRootDir:    "/tmp/sandbox-sessions",
		RPCMaxMessageBytes: 1024,
		APIVersion:         "v1",
		MinAPIVersion:      "v1",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}
