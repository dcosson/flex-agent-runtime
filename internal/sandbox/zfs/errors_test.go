package zfs

import (
	"errors"
	"testing"
)

func TestClassifyError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		stderr   string
		exitCode int
		want     error
	}{
		{name: "dataset not found", stderr: "dataset does not exist", exitCode: 1, want: ErrDatasetNotFound},
		{name: "snapshot exists", stderr: "snapshot already exists", exitCode: 1, want: ErrSnapshotExists},
		{name: "busy", stderr: "dataset is busy", exitCode: 1, want: ErrBusy},
		{name: "permission", stderr: "permission denied", exitCode: 1, want: ErrPermission},
		{name: "pool full", stderr: "pool is full", exitCode: 1, want: ErrPoolFull},
		{name: "no space", stderr: "no space left on device", exitCode: 1, want: ErrNoSpace},
		{name: "command missing", stderr: "not found", exitCode: 127, want: ErrCommandNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyError("TestOp", tc.stderr, tc.exitCode)
			if !errors.Is(got, tc.want) {
				t.Fatalf("classifyError(%q, %d) = %v, want %v", tc.stderr, tc.exitCode, got, tc.want)
			}
		})
	}
}

func TestZFSErrorUnwrap(t *testing.T) {
	t.Parallel()
	err := &ZFSError{Op: "CreateDataset", ExitCode: 1, Stderr: "exists", Err: ErrAlreadyExists}
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("expected wrapped error to match ErrAlreadyExists")
	}
}

func TestClassifyErrorHoldIdempotent(t *testing.T) {
	t.Parallel()
	got := classifyError("HoldSnapshot", "tag already exists on this dataset", 1)
	if got != nil {
		t.Fatalf("expected nil for idempotent hold tag, got %v", got)
	}
}
