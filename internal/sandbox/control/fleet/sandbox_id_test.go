package fleet

import (
	"testing"
)

func TestEncodeSandboxID(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		sessionID  string
		want       string
		wantErr    bool
	}{
		{
			name:       "valid EC2 instance ID",
			instanceID: "i-0abc123def456",
			sessionID:  "sess-7890xyz",
			want:       "fleet:i-0abc123def456:sess-7890xyz",
		},
		{
			name:       "session ID with colons is allowed",
			instanceID: "i-abc",
			sessionID:  "sess:with:colons",
			want:       "fleet:i-abc:sess:with:colons",
		},
		{
			name:       "empty instance ID",
			instanceID: "",
			sessionID:  "sess-1",
			wantErr:    true,
		},
		{
			name:       "empty session ID",
			instanceID: "i-abc",
			sessionID:  "",
			wantErr:    true,
		},
		{
			name:       "both empty",
			instanceID: "",
			sessionID:  "",
			wantErr:    true,
		},
		{
			name:       "instance ID contains colon",
			instanceID: "i-abc:def",
			sessionID:  "sess-1",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeSandboxID(tt.instanceID, tt.sessionID)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeSandboxID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("encodeSandboxID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSandboxID(t *testing.T) {
	tests := []struct {
		name           string
		sandboxID      string
		wantInstanceID string
		wantSessionID  string
		wantErr        bool
	}{
		{
			name:           "valid fleet ID",
			sandboxID:      "fleet:i-0abc123def456:sess-7890xyz",
			wantInstanceID: "i-0abc123def456",
			wantSessionID:  "sess-7890xyz",
		},
		{
			name:           "session ID with colons",
			sandboxID:      "fleet:i-abc:sess:with:colons",
			wantInstanceID: "i-abc",
			wantSessionID:  "sess:with:colons",
		},
		{
			name:      "missing prefix",
			sandboxID: "direct:i-abc:sess-1",
			wantErr:   true,
		},
		{
			name:      "no prefix at all",
			sandboxID: "i-abc:sess-1",
			wantErr:   true,
		},
		{
			name:      "missing separator (no session)",
			sandboxID: "fleet:i-abc",
			wantErr:   true,
		},
		{
			name:      "empty instance ID (colon immediately after prefix)",
			sandboxID: "fleet::sess-1",
			wantErr:   true,
		},
		{
			name:      "empty session ID (trailing colon)",
			sandboxID: "fleet:i-abc:",
			wantErr:   true,
		},
		{
			name:      "empty string",
			sandboxID: "",
			wantErr:   true,
		},
		{
			name:      "prefix only",
			sandboxID: "fleet:",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInst, gotSess, err := parseSandboxID(tt.sandboxID)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseSandboxID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotInst != tt.wantInstanceID {
				t.Errorf("parseSandboxID() instanceID = %q, want %q", gotInst, tt.wantInstanceID)
			}
			if gotSess != tt.wantSessionID {
				t.Errorf("parseSandboxID() sessionID = %q, want %q", gotSess, tt.wantSessionID)
			}
		})
	}
}

func TestSandboxIDRoundTrip(t *testing.T) {
	cases := []struct {
		instanceID string
		sessionID  string
	}{
		{"i-0abc123def456", "sess-7890xyz"},
		{"i-abc", "session-with-dashes"},
		{"1234567890", "s1"},
		{"instance", "sess:with:colons:inside"},
	}

	for _, tc := range cases {
		encoded, err := encodeSandboxID(tc.instanceID, tc.sessionID)
		if err != nil {
			t.Fatalf("encodeSandboxID(%q, %q) unexpected error: %v", tc.instanceID, tc.sessionID, err)
		}

		gotInst, gotSess, err := parseSandboxID(encoded)
		if err != nil {
			t.Fatalf("parseSandboxID(%q) unexpected error: %v", encoded, err)
		}

		if gotInst != tc.instanceID {
			t.Errorf("round-trip instanceID: got %q, want %q", gotInst, tc.instanceID)
		}
		if gotSess != tc.sessionID {
			t.Errorf("round-trip sessionID: got %q, want %q", gotSess, tc.sessionID)
		}
	}
}
