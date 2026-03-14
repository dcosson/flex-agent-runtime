package zfs

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidName = errors.New("zfs: invalid dataset or snapshot name")

	ErrDatasetNotFound  = errors.New("zfs: dataset does not exist")
	ErrDatasetExists    = errors.New("zfs: dataset already exists")
	ErrSnapshotNotFound = errors.New("zfs: snapshot does not exist")
	ErrSnapshotExists   = errors.New("zfs: snapshot already exists")
	ErrSnapshotHeld     = errors.New("zfs: snapshot has holds and cannot be destroyed")
	ErrHasClones        = errors.New("zfs: dataset has dependent clones")
	ErrPoolNotFound     = errors.New("zfs: pool does not exist")
	ErrPoolFaulted      = errors.New("zfs: pool is in faulted state")

	ErrPermission      = errors.New("zfs: permission denied")
	ErrBusy            = errors.New("zfs: dataset is busy")
	ErrPoolFull        = errors.New("zfs: pool is full")
	ErrNoSpace         = errors.New("zfs: no space left on device")
	ErrCommandNotFound = errors.New("zfs: zfs/zpool command not found")

	// Backward-compatible coarse sentinels.
	ErrNotFound      = errors.New("zfs: object does not exist")
	ErrAlreadyExists = errors.New("zfs: object already exists")
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

func wrapNotFound(specific error) error {
	return errors.Join(ErrNotFound, specific)
}

func wrapExists(specific error) error {
	return errors.Join(ErrAlreadyExists, specific)
}

func classifyError(op, stderr string, exitCode int) error {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "tag already exists on this dataset"):
		// Hold is idempotent for existing tag.
		return nil
	case strings.Contains(lower, "dataset does not exist"):
		return wrapNotFound(ErrDatasetNotFound)
	case strings.Contains(lower, "snapshot does not exist"),
		strings.Contains(lower, "could not find any snapshots"),
		strings.Contains(lower, "no such tag on this dataset"):
		return wrapNotFound(ErrSnapshotNotFound)
	case strings.Contains(lower, "no such pool"):
		return wrapNotFound(ErrPoolNotFound)
	case strings.Contains(lower, "dataset already exists"):
		return wrapExists(ErrDatasetExists)
	case strings.Contains(lower, "snapshot already exists"):
		return wrapExists(ErrSnapshotExists)
	case strings.Contains(lower, "already exists"):
		return ErrAlreadyExists
	case strings.Contains(lower, "dataset has dependent clones"):
		return ErrHasClones
	case strings.Contains(lower, "snapshot has holds"):
		return ErrSnapshotHeld
	case strings.Contains(lower, "dataset is busy"), strings.Contains(lower, "resource busy"):
		return ErrBusy
	case strings.Contains(lower, "permission denied"), strings.Contains(lower, "operation not permitted"):
		return ErrPermission
	case strings.Contains(lower, "pool is faulted"), strings.Contains(lower, "state is faulted"):
		return ErrPoolFaulted
	case strings.Contains(lower, "pool is full"):
		return ErrPoolFull
	case strings.Contains(lower, "no space left"), strings.Contains(lower, "out of space"):
		return ErrNoSpace
	case exitCode == 127:
		return ErrCommandNotFound
	default:
		return fmt.Errorf("zfs %s failed: %s", op, strings.TrimSpace(stderr))
	}
}
