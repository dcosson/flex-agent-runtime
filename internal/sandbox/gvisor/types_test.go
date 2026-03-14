package gvisor

import (
	"strings"
	"testing"
	"time"
)

func TestContainerOptions_Validate(t *testing.T) {
	valid := ContainerOptions{
		Command: []string{"echo", "hello"},
		WorkDir: "/workspace",
		RootFS:  "/rootfs",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid options should pass: %v", err)
	}

	tests := []struct {
		name    string
		modify  func(*ContainerOptions)
		wantErr string
	}{
		{
			name:    "empty command",
			modify:  func(o *ContainerOptions) { o.Command = nil },
			wantErr: "command is required",
		},
		{
			name:    "empty rootfs",
			modify:  func(o *ContainerOptions) { o.RootFS = "" },
			wantErr: "rootfs path is required",
		},
		{
			name:    "empty workdir",
			modify:  func(o *ContainerOptions) { o.WorkDir = "" },
			wantErr: "working directory is required",
		},
		{
			name:    "relative workdir",
			modify:  func(o *ContainerOptions) { o.WorkDir = "relative" },
			wantErr: "must be absolute",
		},
		{
			name: "negative CPUs",
			modify: func(o *ContainerOptions) {
				o.Resources.CPUs = -1
			},
			wantErr: "non-negative",
		},
		{
			name: "excessive CPUs",
			modify: func(o *ContainerOptions) {
				o.Resources.CPUs = 300
			},
			wantErr: "exceeds maximum",
		},
		{
			name: "negative timeout",
			modify: func(o *ContainerOptions) {
				o.Resources.Timeout = -1 * time.Second
			},
			wantErr: "non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := valid
			tt.modify(&opts)
			err := opts.Validate()
			if err == nil {
				t.Fatal("expected error")
			}
			if tt.wantErr != "" {
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error %q should contain %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestContainerStatus_Values(t *testing.T) {
	statuses := []ContainerStatus{
		StatusExited,
		StatusTimedOut,
		StatusOOMKilled,
		StatusKilled,
		StatusError,
	}

	seen := make(map[ContainerStatus]bool)
	for _, s := range statuses {
		if seen[s] {
			t.Errorf("duplicate status: %s", s)
		}
		seen[s] = true
		if s == "" {
			t.Error("empty status value")
		}
	}
}

func TestNetworkMode_Values(t *testing.T) {
	modes := []NetworkMode{NetworkNone, NetworkSandbox, NetworkHost}
	for _, m := range modes {
		if m == "" {
			t.Error("empty network mode")
		}
	}
}

func TestConstants(t *testing.T) {
	if DefaultTimeout <= 0 {
		t.Error("DefaultTimeout should be positive")
	}
	if DefaultMemoryMB <= 0 {
		t.Error("DefaultMemoryMB should be positive")
	}
	if DefaultMaxPIDs <= 0 {
		t.Error("DefaultMaxPIDs should be positive")
	}
	if DefaultMaxOutput <= 0 {
		t.Error("DefaultMaxOutput should be positive")
	}
	if MaxContainerNameLen <= 0 {
		t.Error("MaxContainerNameLen should be positive")
	}
}
