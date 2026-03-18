package instance

import (
	"testing"
	"time"
)

// --- CloudInstanceState string stability tests ---

func TestCloudInstanceState_StringValues(t *testing.T) {
	// These string values are part of the public API contract. If they change,
	// serialized state (logs, metrics, persisted fleet state) will break.
	// This test ensures nobody accidentally changes a constant value.
	tests := []struct {
		state CloudInstanceState
		want  string
	}{
		{CloudInstancePending, "pending"},
		{CloudInstanceRunning, "running"},
		{CloudInstanceStopping, "stopping"},
		{CloudInstanceStopped, "stopped"},
		{CloudInstanceTerminating, "terminating"},
		{CloudInstanceTerminated, "terminated"},
	}

	for _, tt := range tests {
		if got := string(tt.state); got != tt.want {
			t.Errorf("CloudInstanceState = %q, want %q", got, tt.want)
		}
	}
}

func TestCloudInstanceState_AllStatesExhaustive(t *testing.T) {
	// Verify AllCloudInstanceStates returns exactly the expected set.
	all := AllCloudInstanceStates()
	expected := map[CloudInstanceState]bool{
		CloudInstancePending:     true,
		CloudInstanceRunning:     true,
		CloudInstanceStopping:    true,
		CloudInstanceStopped:     true,
		CloudInstanceTerminating: true,
		CloudInstanceTerminated:  true,
	}

	if len(all) != len(expected) {
		t.Fatalf("AllCloudInstanceStates returned %d states, want %d", len(all), len(expected))
	}

	seen := make(map[CloudInstanceState]bool)
	for _, s := range all {
		if !expected[s] {
			t.Errorf("unexpected state in AllCloudInstanceStates: %q", s)
		}
		if seen[s] {
			t.Errorf("duplicate state in AllCloudInstanceStates: %q", s)
		}
		seen[s] = true
	}
}

func TestCloudInstanceState_StableOrdering(t *testing.T) {
	// AllCloudInstanceStates must return states in a consistent order across calls.
	first := AllCloudInstanceStates()
	second := AllCloudInstanceStates()

	if len(first) != len(second) {
		t.Fatalf("length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("index %d: %q != %q", i, first[i], second[i])
		}
	}
}

// --- InstanceFilter construction tests ---

func TestInstanceFilter_EmptyFilter(t *testing.T) {
	f := InstanceFilter{}
	if f.Tags != nil {
		t.Error("zero-value InstanceFilter should have nil Tags")
	}
	if f.States != nil {
		t.Error("zero-value InstanceFilter should have nil States")
	}
}

func TestInstanceFilter_WithTags(t *testing.T) {
	f := InstanceFilter{
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
			"fleet-id":  "test-fleet-1",
		},
	}
	if len(f.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(f.Tags))
	}
	if f.Tags["ManagedBy"] != "flex-agent-runtime" {
		t.Errorf("ManagedBy = %q, want %q", f.Tags["ManagedBy"], "flex-agent-runtime")
	}
	if f.Tags["fleet-id"] != "test-fleet-1" {
		t.Errorf("fleet-id = %q, want %q", f.Tags["fleet-id"], "test-fleet-1")
	}
}

func TestInstanceFilter_WithStates(t *testing.T) {
	f := InstanceFilter{
		States: []CloudInstanceState{CloudInstanceRunning, CloudInstancePending},
	}
	if len(f.States) != 2 {
		t.Fatalf("expected 2 states, got %d", len(f.States))
	}
	if f.States[0] != CloudInstanceRunning {
		t.Errorf("States[0] = %q, want %q", f.States[0], CloudInstanceRunning)
	}
	if f.States[1] != CloudInstancePending {
		t.Errorf("States[1] = %q, want %q", f.States[1], CloudInstancePending)
	}
}

func TestInstanceFilter_FullFilter(t *testing.T) {
	f := InstanceFilter{
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
		},
		States: []CloudInstanceState{
			CloudInstanceRunning,
			CloudInstanceStopped,
		},
	}
	if len(f.Tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(f.Tags))
	}
	if len(f.States) != 2 {
		t.Errorf("expected 2 states, got %d", len(f.States))
	}
}

// --- InstanceConfig construction tests ---

func TestInstanceConfig_ZeroValue(t *testing.T) {
	cfg := InstanceConfig{}
	if cfg.Image != "" {
		t.Error("zero-value Image should be empty")
	}
	if cfg.InstanceType != "" {
		t.Error("zero-value InstanceType should be empty")
	}
	if cfg.DiskSizeGB != 0 {
		t.Error("zero-value DiskSizeGB should be 0")
	}
}

func TestInstanceConfig_WithAllFields(t *testing.T) {
	cfg := InstanceConfig{
		Image:        "ami-12345678",
		InstanceType: "m5.xlarge",
		UserData:     "#!/bin/bash\necho hello",
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
			"fleet-id":  "fleet-abc",
		},
		DiskSizeGB: 100,
	}
	if cfg.Image != "ami-12345678" {
		t.Errorf("Image = %q", cfg.Image)
	}
	if cfg.InstanceType != "m5.xlarge" {
		t.Errorf("InstanceType = %q", cfg.InstanceType)
	}
	if cfg.UserData != "#!/bin/bash\necho hello" {
		t.Errorf("UserData = %q", cfg.UserData)
	}
	if len(cfg.Tags) != 2 {
		t.Errorf("len(Tags) = %d", len(cfg.Tags))
	}
	if cfg.DiskSizeGB != 100 {
		t.Errorf("DiskSizeGB = %d", cfg.DiskSizeGB)
	}
}

// --- InstanceInfo tests ---

func TestInstanceInfo_WithLaunchTime(t *testing.T) {
	now := time.Now()
	info := InstanceInfo{
		InstanceID: "i-abc123",
		PrivateIP:  "10.0.1.5",
		PublicIP:   "54.123.45.67",
		State:      CloudInstanceRunning,
		LaunchTime: now,
		Tags: map[string]string{
			"Name": "test-instance",
		},
	}
	if info.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q", info.InstanceID)
	}
	if info.State != CloudInstanceRunning {
		t.Errorf("State = %q", info.State)
	}
	if info.LaunchTime != now {
		t.Error("LaunchTime mismatch")
	}
	if info.Tags["Name"] != "test-instance" {
		t.Errorf("Tags[Name] = %q", info.Tags["Name"])
	}
}

// --- InstanceStatus tests ---

func TestInstanceStatus_WithStateReason(t *testing.T) {
	status := InstanceStatus{
		InstanceID:  "i-abc123",
		State:       CloudInstanceStopped,
		StateReason: "Client.UserInitiatedShutdown",
		PrivateIP:   "10.0.1.5",
		PublicIP:    "",
	}
	if status.StateReason != "Client.UserInitiatedShutdown" {
		t.Errorf("StateReason = %q", status.StateReason)
	}
	if status.PublicIP != "" {
		t.Errorf("PublicIP should be empty for stopped instance, got %q", status.PublicIP)
	}
}
