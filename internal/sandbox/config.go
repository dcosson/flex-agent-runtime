package sandbox

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// StorageBackend identifies the session storage mechanism.
type StorageBackend string

const (
	StorageBackendZFS       StorageBackend = "zfs"
	StorageBackendLocalDisk StorageBackend = "local-disk"
)

// ContainerRuntime identifies the runtime for Tier 2 tool execution.
type ContainerRuntime string

const (
	ContainerRuntimeGVisor ContainerRuntime = "gvisor"
	ContainerRuntimeNone   ContainerRuntime = "none"
)

var sessionIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func validateSessionIDForPath(sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid session_id %q: must match %s", sessionID, sessionIDPattern.String())
	}
	if strings.Contains(sessionID, "..") {
		return fmt.Errorf("invalid session_id %q: path traversal segment", sessionID)
	}
	return nil
}

func safeSessionPath(rootDir, sessionID string) (string, error) {
	if err := validateSessionIDForPath(sessionID); err != nil {
		return "", err
	}
	cleanRoot := filepath.Clean(rootDir)
	target := filepath.Clean(filepath.Join(cleanRoot, sessionID))
	rel, err := filepath.Rel(cleanRoot, target)
	if err != nil {
		return "", fmt.Errorf("resolve session path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("session path escapes sessions root: %q", sessionID)
	}
	return target, nil
}
