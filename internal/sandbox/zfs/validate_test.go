package zfs

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	t.Parallel()
	if err := ValidateName("pool/sessions/sess-1"); err != nil {
		t.Fatalf("ValidateName(valid) error = %v", err)
	}

	cases := []string{"", "../x", "/leading", "trailing/", "pool//x", "pool/x@bad"}
	for _, tc := range cases {
		t.Run(tc, func(t *testing.T) {
			err := ValidateName(tc)
			if !errors.Is(err, ErrInvalidName) {
				t.Fatalf("expected ErrInvalidName, got %v", err)
			}
		})
	}
}

func TestValidateSnapshotFullName(t *testing.T) {
	t.Parallel()
	if err := ValidateSnapshotFullName("pool/ds@turn-001"); err != nil {
		t.Fatalf("expected valid full snapshot name, got %v", err)
	}
	if err := ValidateSnapshotFullName("pool/ds"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestValidateMountpoint(t *testing.T) {
	t.Parallel()
	if err := ValidateMountpoint("/mnt/ai/session-1"); err != nil {
		t.Fatalf("expected valid mountpoint, got %v", err)
	}
	if err := ValidateMountpoint("relative/path"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestValidatePropertyName(t *testing.T) {
	t.Parallel()
	if err := ValidatePropertyName("com.example:team"); err != nil {
		t.Fatalf("expected valid property, got %v", err)
	}
	err := ValidatePropertyName("exec")
	if !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
	if !strings.Contains(err.Error(), "restricted") {
		t.Fatalf("expected restricted in error, got %v", err)
	}
}
