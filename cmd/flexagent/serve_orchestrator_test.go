package main

import (
	"strings"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/fleet"
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

func TestServeOrchestratorConfigValidateSandboxHostAccepted(t *testing.T) {
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
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() unexpected error: %v", err)
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

func TestServeOrchestratorConfigValidateFleetModeAccepted(t *testing.T) {
	cfg := serveOrchestratorConfig{
		ListenAddr:           ":8080",
		SandboxHostAddr:      "host1:8082,host2:8082",
		ShutdownTimeout:      30 * time.Second,
		HealthCheckInterval:  15 * time.Second,
		CreateSessionTimeout: 2 * time.Minute,
		RPCMaxMessageBytes:   1024,
		APIVersion:           "v1",
		MinAPIVersion:        "v1",
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() unexpected error for multiple sandbox-host addresses: %v", err)
	}
}

func TestBuildNodeOrFleetControlFleetMode(t *testing.T) {
	cfg := serveOrchestratorConfig{
		APIVersion:          "v1",
		HealthCheckInterval: 5 * time.Second,
	}
	control, err := buildNodeOrFleetControl([]string{"host1:8082", "host2:8082"}, cfg, nil)
	if err != nil {
		t.Fatalf("buildNodeOrFleetControl() error = %v", err)
	}
	if _, ok := control.(*fleet.FleetSandboxControl); !ok {
		t.Fatalf("buildNodeOrFleetControl() type = %T, want *fleet.FleetSandboxControl", control)
	}
}

func TestBuildNodeOrFleetControlFleetModeRejectsMixedPorts(t *testing.T) {
	cfg := serveOrchestratorConfig{
		APIVersion:          "v1",
		HealthCheckInterval: 5 * time.Second,
	}
	_, err := buildNodeOrFleetControl([]string{"host1:8082", "host2:8083"}, cfg, nil)
	if err == nil {
		t.Fatal("buildNodeOrFleetControl() expected error for mixed ports")
	}
	if !strings.Contains(err.Error(), "same port") {
		t.Fatalf("buildNodeOrFleetControl() error = %v, want same-port validation", err)
	}
}

func TestParseSandboxHostAddr(t *testing.T) {
	t.Run("host port", func(t *testing.T) {
		got, err := parseSandboxHostAddr("10.0.0.10:8082")
		if err != nil {
			t.Fatalf("parseSandboxHostAddr() error = %v", err)
		}
		if got.host != "10.0.0.10" || got.port != 8082 {
			t.Fatalf("parseSandboxHostAddr() = %+v, want host=10.0.0.10 port=8082", got)
		}
	})

	t.Run("url", func(t *testing.T) {
		got, err := parseSandboxHostAddr("http://10.0.0.11:8082")
		if err != nil {
			t.Fatalf("parseSandboxHostAddr() error = %v", err)
		}
		if got.host != "10.0.0.11" || got.port != 8082 {
			t.Fatalf("parseSandboxHostAddr() = %+v, want host=10.0.0.11 port=8082", got)
		}
	})

	t.Run("missing port", func(t *testing.T) {
		_, err := parseSandboxHostAddr("host1")
		if err == nil {
			t.Fatal("parseSandboxHostAddr() expected error for missing port")
		}
	})

	t.Run("url path not allowed", func(t *testing.T) {
		_, err := parseSandboxHostAddr("http://host1:8082/path")
		if err == nil {
			t.Fatal("parseSandboxHostAddr() expected error for URL path")
		}
	})
}
