package main

import (
	"os"
	"testing"
	"time"
)

func TestEnvParsers(t *testing.T) {
	t.Setenv("TEST_ENV_INT", "42")
	t.Setenv("TEST_ENV_INT64", "1234")
	t.Setenv("TEST_ENV_DURATION", "30s")
	t.Setenv("TEST_ENV_BOOL", "true")
	t.Setenv("TEST_ENV_STRING", "hello")

	if got := envIntOrDefault("TEST_ENV_INT", 0); got != 42 {
		t.Fatalf("envIntOrDefault = %d, want 42", got)
	}
	if got := envInt64OrDefault("TEST_ENV_INT64", 0); got != 1234 {
		t.Fatalf("envInt64OrDefault = %d, want 1234", got)
	}
	if got := envDurationOrDefault("TEST_ENV_DURATION", 0); got != 30*time.Second {
		t.Fatalf("envDurationOrDefault = %s, want 30s", got)
	}
	if got := envBoolOrDefault("TEST_ENV_BOOL", false); !got {
		t.Fatalf("envBoolOrDefault = false, want true")
	}
	if got := envOrDefault("TEST_ENV_STRING", "x"); got != "hello" {
		t.Fatalf("envOrDefault = %q, want hello", got)
	}
}

func TestEnvParsersFallback(t *testing.T) {
	const missing = "FLEXAGENT_MISSING_ENV_FOR_TEST"
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

func TestEnvParsersInvalidFallback(t *testing.T) {
	t.Setenv("TEST_BAD_INT", "notanumber")
	t.Setenv("TEST_BAD_INT64", "notanumber")
	t.Setenv("TEST_BAD_DURATION", "notaduration")
	t.Setenv("TEST_BAD_BOOL", "notabool")

	if got := envIntOrDefault("TEST_BAD_INT", 99); got != 99 {
		t.Fatalf("bad int = %d, want 99", got)
	}
	if got := envInt64OrDefault("TEST_BAD_INT64", 88); got != 88 {
		t.Fatalf("bad int64 = %d, want 88", got)
	}
	if got := envDurationOrDefault("TEST_BAD_DURATION", 5*time.Second); got != 5*time.Second {
		t.Fatalf("bad duration = %s, want 5s", got)
	}
	if got := envBoolOrDefault("TEST_BAD_BOOL", true); !got {
		t.Fatalf("bad bool = false, want true")
	}
}
