package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseServeOrchestratorConfigDefaults(t *testing.T) {
	cfg := parseServeOrchestratorConfig(nil)
	if cfg.ListenAddr != ":8080" {
		t.Fatalf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.DirectInstanceType != "t3.medium" {
		t.Fatalf("DirectInstanceType = %q, want t3.medium", cfg.DirectInstanceType)
	}
	if cfg.MaxSessions != 0 {
		t.Fatalf("MaxSessions = %d, want 0", cfg.MaxSessions)
	}
	if cfg.AgentMaxSessions != 0 {
		t.Fatalf("AgentMaxSessions = %d, want 0", cfg.AgentMaxSessions)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.CreateSessionTimeout != 3*time.Minute {
		t.Fatalf("CreateSessionTimeout = %s, want 3m", cfg.CreateSessionTimeout)
	}
	if cfg.HealthCheckInterval != 15*time.Second {
		t.Fatalf("HealthCheckInterval = %s, want 15s", cfg.HealthCheckInterval)
	}
}

func TestParseServeOrchestratorConfigFlags(t *testing.T) {
	cfg := parseServeOrchestratorConfig([]string{
		"-listen", ":9999",
		"-direct-ami-id", "ami-123",
		"-direct-subnet-id", "subnet-123",
		"-direct-security-group-ids", "sg-a,sg-b",
		"-max-sessions", "7",
		"-agent-max-sessions", "5",
		"-create-session-timeout", "45s",
		"-shutdown-timeout", "20s",
		"-rpc-max-message-bytes", "1234",
		"-api-version", "v2",
		"-min-api-version", "v1",
	})
	if cfg.ListenAddr != ":9999" {
		t.Fatalf("ListenAddr = %q, want :9999", cfg.ListenAddr)
	}
	if cfg.DirectAMIID != "ami-123" {
		t.Fatalf("DirectAMIID = %q, want ami-123", cfg.DirectAMIID)
	}
	if cfg.DirectSubnetID != "subnet-123" {
		t.Fatalf("DirectSubnetID = %q, want subnet-123", cfg.DirectSubnetID)
	}
	if got := cfg.directSecurityGroupIDs(); len(got) != 2 || got[0] != "sg-a" || got[1] != "sg-b" {
		t.Fatalf("directSecurityGroupIDs() = %#v, want [sg-a sg-b]", got)
	}
	if cfg.MaxSessions != 7 {
		t.Fatalf("MaxSessions = %d, want 7", cfg.MaxSessions)
	}
	if cfg.AgentMaxSessions != 5 {
		t.Fatalf("AgentMaxSessions = %d, want 5", cfg.AgentMaxSessions)
	}
	if cfg.CreateSessionTimeout != 45*time.Second {
		t.Fatalf("CreateSessionTimeout = %s, want 45s", cfg.CreateSessionTimeout)
	}
	if cfg.ShutdownTimeout != 20*time.Second {
		t.Fatalf("ShutdownTimeout = %s, want 20s", cfg.ShutdownTimeout)
	}
	if cfg.RPCMaxMessageBytes != 1234 {
		t.Fatalf("RPCMaxMessageBytes = %d, want 1234", cfg.RPCMaxMessageBytes)
	}
	if cfg.APIVersion != "v2" {
		t.Fatalf("APIVersion = %q, want v2", cfg.APIVersion)
	}
}

func TestServeOrchestratorConfigValidateValidDirect(t *testing.T) {
	cfg := serveOrchestratorConfig{
		ListenAddr:                ":8080",
		DirectAMIID:               "ami-123",
		DirectSubnetID:            "subnet-123",
		DirectSecurityGroupIDsRaw: "sg-a",
		DirectInstanceType:        "t3.medium",
		MaxSessions:               1,
		AgentMaxSessions:          0,
		ShutdownTimeout:           30 * time.Second,
		HealthCheckInterval:       15 * time.Second,
		CreateSessionTimeout:      2 * time.Minute,
		RPCMaxMessageBytes:        1024,
		APIVersion:                "v1",
		MinAPIVersion:             "v1",
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
	}
}

func TestServeOrchestratorConfigValidateErrors(t *testing.T) {
	cfg := serveOrchestratorConfig{}
	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() expected error")
	}
	checks := []string{
		"listen is required",
		"rpc-max-message-bytes must be > 0",
		"api-version is required",
		"min-api-version is required",
		"create-session-timeout must be > 0",
		"shutdown-timeout must be > 0",
		"health-check-interval must be > 0",
		"at least one backend must be configured",
	}
	for _, want := range checks {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validate() missing %q in %v", want, err)
		}
	}
}

func TestServeOrchestratorConfigValidateSandboxHostUnsupported(t *testing.T) {
	cfg := serveOrchestratorConfig{
		ListenAddr:           ":8080",
		SandboxHostAddr:      "10.0.0.2:8080",
		ShutdownTimeout:      30 * time.Second,
		HealthCheckInterval:  15 * time.Second,
		CreateSessionTimeout: 2 * time.Minute,
		RPCMaxMessageBytes:   1024,
		APIVersion:           "v1",
		MinAPIVersion:        "v1",
	}
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "sandbox-host modes are not implemented") {
		t.Fatalf("validate() error = %v, want sandbox-host unsupported", err)
	}
}

func TestSplitCommaList(t *testing.T) {
	got := splitCommaList("a, b,,c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("splitCommaList() = %#v, want [a b c]", got)
	}
}

func TestInstanceProfileName(t *testing.T) {
	if got := instanceProfileName("arn:aws:iam::123456789012:instance-profile/MyProfile"); got != "MyProfile" {
		t.Fatalf("instanceProfileName(arn) = %q, want MyProfile", got)
	}
	if got := instanceProfileName("my-profile"); got != "my-profile" {
		t.Fatalf("instanceProfileName(raw) = %q, want my-profile", got)
	}
}
