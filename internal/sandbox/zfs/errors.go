package zfs

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidName     = errors.New("zfs: invalid dataset or snapshot name")
	ErrNotFound        = errors.New("zfs: object does not exist")
	ErrAlreadyExists   = errors.New("zfs: object already exists")
	ErrBusy            = errors.New("zfs: dataset is busy")
	ErrPermission      = errors.New("zfs: permission denied")
	ErrPoolFull        = errors.New("zfs: pool is full")
	ErrNoSpace         = errors.New("zfs: no space left on device")
	ErrCommandNotFound = errors.New("zfs: zfs/zpool command not found")
)

// ZFSError wraps a zfs/zpool command failure with structured context.
type ZFSError struct {
	Op       string
	Command  string
	Dataset  string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *ZFSError) Error() string {
	if e == nil {
		return "zfs: <nil>"
	}
	if e.Stderr == "" {
		return fmt.Sprintf("zfs %s failed (exit=%d)", e.Op, e.ExitCode)
	}
	return fmt.Sprintf("zfs %s failed (exit=%d): %s", e.Op, e.ExitCode, strings.TrimSpace(e.Stderr))
}

func (e *ZFSError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func classifyError(stderr string, exitCode int) error {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "does not exist"), strings.Contains(lower, "dataset does not exist"), strings.Contains(lower, "snapshot does not exist"), strings.Contains(lower, "no such pool"):
		return ErrNotFound
	case strings.Contains(lower, "already exists"):
		return ErrAlreadyExists
	case strings.Contains(lower, "dataset is busy"), strings.Contains(lower, "resource busy"):
		return ErrBusy
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "operation not permitted"):
		return ErrPermission
	case strings.Contains(lower, "pool is full"):
		return ErrPoolFull
	case strings.Contains(lower, "no space left"), strings.Contains(lower, "out of space"):
		return ErrNoSpace
	case exitCode == 127:
		return ErrCommandNotFound
	default:
		return fmt.Errorf("zfs command failed: %s", strings.TrimSpace(stderr))
	}
}
