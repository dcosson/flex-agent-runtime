package direct

import (
	"errors"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

func TestSelectInstanceType(t *testing.T) {
	tests := []struct {
		name        string
		resources   control.ResourceSpec
		defaultType string
		want        string
		wantErr     error
	}{
		{
			name:        "zero resources uses default",
			resources:   control.ResourceSpec{CPUs: 0, MemMB: 0},
			defaultType: "t3.medium",
			want:        "t3.medium",
		},
		{
			name:        "zero resources uses custom default",
			resources:   control.ResourceSpec{CPUs: 0, MemMB: 0},
			defaultType: "m5.xlarge",
			want:        "m5.xlarge",
		},
		{
			name:        "1 CPU 2GB -> t3.medium",
			resources:   control.ResourceSpec{CPUs: 1, MemMB: 2048},
			defaultType: "t3.medium",
			want:        "t3.medium",
		},
		{
			name:        "2 CPU 4GB -> t3.medium",
			resources:   control.ResourceSpec{CPUs: 2, MemMB: 4096},
			defaultType: "t3.medium",
			want:        "t3.medium",
		},
		{
			name:        "3 CPU 6GB -> t3.large",
			resources:   control.ResourceSpec{CPUs: 3, MemMB: 6144},
			defaultType: "t3.medium",
			want:        "t3.large",
		},
		{
			name:        "4 CPU 8GB -> t3.large",
			resources:   control.ResourceSpec{CPUs: 4, MemMB: 8192},
			defaultType: "t3.medium",
			want:        "t3.large",
		},
		{
			name:        "5 CPU 12GB -> t3.xlarge",
			resources:   control.ResourceSpec{CPUs: 5, MemMB: 12288},
			defaultType: "t3.medium",
			want:        "t3.xlarge",
		},
		{
			name:        "8 CPU 16GB -> t3.xlarge",
			resources:   control.ResourceSpec{CPUs: 8, MemMB: 16384},
			defaultType: "t3.medium",
			want:        "t3.xlarge",
		},
		{
			name:        "10 CPU 24GB -> m5.2xlarge",
			resources:   control.ResourceSpec{CPUs: 10, MemMB: 24576},
			defaultType: "t3.medium",
			want:        "m5.2xlarge",
		},
		{
			name:        "16 CPU 32GB -> m5.2xlarge",
			resources:   control.ResourceSpec{CPUs: 16, MemMB: 32768},
			defaultType: "t3.medium",
			want:        "m5.2xlarge",
		},
		{
			name:        "20 CPU 48GB -> m5.4xlarge",
			resources:   control.ResourceSpec{CPUs: 20, MemMB: 49152},
			defaultType: "t3.medium",
			want:        "m5.4xlarge",
		},
		{
			name:        "32 CPU 64GB -> m5.4xlarge",
			resources:   control.ResourceSpec{CPUs: 32, MemMB: 65536},
			defaultType: "t3.medium",
			want:        "m5.4xlarge",
		},
		{
			name:        "exceeds max CPU",
			resources:   control.ResourceSpec{CPUs: 64, MemMB: 32768},
			defaultType: "t3.medium",
			wantErr:     ErrResourcesExceedMaximum,
		},
		{
			name:        "exceeds max memory",
			resources:   control.ResourceSpec{CPUs: 4, MemMB: 131072},
			defaultType: "t3.medium",
			wantErr:     ErrResourcesExceedMaximum,
		},
		{
			name:        "exceeds both",
			resources:   control.ResourceSpec{CPUs: 128, MemMB: 262144},
			defaultType: "t3.medium",
			wantErr:     ErrResourcesExceedMaximum,
		},
		{
			name:        "only CPU specified (mem zero) -> fits smallest matching CPU tier",
			resources:   control.ResourceSpec{CPUs: 6, MemMB: 0},
			defaultType: "t3.medium",
			want:        "t3.xlarge",
		},
		{
			name:        "only memory specified (CPU zero) -> fits smallest matching mem tier",
			resources:   control.ResourceSpec{CPUs: 0, MemMB: 12000},
			defaultType: "t3.medium",
			want:        "t3.xlarge",
		},
		{
			name:        "small CPU large mem -> memory determines tier",
			resources:   control.ResourceSpec{CPUs: 1, MemMB: 30000},
			defaultType: "t3.medium",
			want:        "m5.2xlarge",
		},
		{
			name:        "large CPU small mem -> CPU determines tier",
			resources:   control.ResourceSpec{CPUs: 12, MemMB: 2048},
			defaultType: "t3.medium",
			want:        "m5.2xlarge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectInstanceType(tt.resources, tt.defaultType)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("selectInstanceType(%+v, %q) returned nil error, want %v", tt.resources, tt.defaultType, tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("selectInstanceType(%+v, %q) error = %v, want %v", tt.resources, tt.defaultType, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectInstanceType(%+v, %q) returned unexpected error: %v", tt.resources, tt.defaultType, err)
			}
			if got != tt.want {
				t.Errorf("selectInstanceType(%+v, %q) = %q, want %q", tt.resources, tt.defaultType, got, tt.want)
			}
		})
	}
}

func TestInstanceTypeMappingsOrdered(t *testing.T) {
	// Verify the mapping table is ordered from smallest to largest
	for i := 1; i < len(instanceTypeMappings); i++ {
		prev := instanceTypeMappings[i-1]
		curr := instanceTypeMappings[i]
		if curr.maxCPUs < prev.maxCPUs {
			t.Errorf("instanceTypeMappings not ordered by maxCPUs: [%d]=%v > [%d]=%v", i-1, prev.maxCPUs, i, curr.maxCPUs)
		}
		if curr.maxMemMB < prev.maxMemMB {
			t.Errorf("instanceTypeMappings not ordered by maxMemMB: [%d]=%v > [%d]=%v", i-1, prev.maxMemMB, i, curr.maxMemMB)
		}
	}
}
