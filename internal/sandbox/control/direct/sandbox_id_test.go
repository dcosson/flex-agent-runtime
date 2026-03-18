package direct

import (
	"strings"
	"testing"
)

func TestEncodeSandboxID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		want       string
	}{
		{
			name:       "standard instance ID",
			instanceID: "i-0abc123def456",
			want:       "direct:i-0abc123def456",
		},
		{
			name:       "longer instance ID",
			instanceID: "i-0abcdef1234567890",
			want:       "direct:i-0abcdef1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeSandboxID(tt.instanceID)
			if got != tt.want {
				t.Errorf("encodeSandboxID(%q) = %q, want %q", tt.instanceID, got, tt.want)
			}
		})
	}
}

func TestParseSandboxID(t *testing.T) {
	tests := []struct {
		name      string
		sandboxID string
		want      string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid direct sandbox ID",
			sandboxID: "direct:i-0abc123def456",
			want:      "i-0abc123def456",
		},
		{
			name:      "valid direct sandbox ID with long instance ID",
			sandboxID: "direct:i-0abcdef1234567890",
			want:      "i-0abcdef1234567890",
		},
		{
			name:      "wrong prefix fleet",
			sandboxID: "fleet:i-0abc123:sess-xyz",
			wantErr:   true,
			errMsg:    "invalid sandbox ID prefix",
		},
		{
			name:      "wrong prefix node",
			sandboxID: "node:sess-abc",
			wantErr:   true,
			errMsg:    "invalid sandbox ID prefix",
		},
		{
			name:      "no prefix at all",
			sandboxID: "i-0abc123def456",
			wantErr:   true,
			errMsg:    "invalid sandbox ID prefix",
		},
		{
			name:      "empty string",
			sandboxID: "",
			wantErr:   true,
			errMsg:    "invalid sandbox ID prefix",
		},
		{
			name:      "just the prefix with no instance ID",
			sandboxID: "direct:",
			wantErr:   true,
			errMsg:    "empty instance ID",
		},
		{
			name:      "prefix with colon but different prefix",
			sandboxID: "e2b:some-id",
			wantErr:   true,
			errMsg:    "invalid sandbox ID prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSandboxID(tt.sandboxID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSandboxID(%q) returned nil error, want error containing %q", tt.sandboxID, tt.errMsg)
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("parseSandboxID(%q) error = %q, want error containing %q", tt.sandboxID, err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSandboxID(%q) returned unexpected error: %v", tt.sandboxID, err)
			}
			if got != tt.want {
				t.Errorf("parseSandboxID(%q) = %q, want %q", tt.sandboxID, got, tt.want)
			}
		})
	}
}

func TestSandboxIDRoundTrip(t *testing.T) {
	instanceIDs := []string{
		"i-0abc123def456",
		"i-0abcdef1234567890",
		"i-1234567890abcdef0",
	}

	for _, id := range instanceIDs {
		t.Run(id, func(t *testing.T) {
			encoded := encodeSandboxID(id)
			decoded, err := parseSandboxID(encoded)
			if err != nil {
				t.Fatalf("round-trip failed for %q: encode=%q, parse error: %v", id, encoded, err)
			}
			if decoded != id {
				t.Errorf("round-trip mismatch for %q: encode=%q, decoded=%q", id, encoded, decoded)
			}
		})
	}
}
