package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveSafePath canonicalizes path and ensures it stays within rootDir.
// Follows symlinks during resolution to prevent symlink-based escapes.
func resolveSafePath(rootDir, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	// Make path absolute relative to rootDir
	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(rootDir, path))
	}

	// Resolve symlinks to get the real path. If the file (or parent dirs)
	// don't exist yet, walk up to the nearest existing ancestor and resolve
	// from there. This ensures path traversal is detected even when
	// intermediate directories don't exist.
	resolved, err := resolveWithAncestors(abs)
	if err != nil {
		return "", err
	}

	// Resolve root dir symlinks too for consistent comparison
	resolvedRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return "", fmt.Errorf("root dir not accessible: %w", err)
	}

	// Ensure resolved path is under resolved root
	if !isUnderRoot(resolved, resolvedRoot) {
		return "", fmt.Errorf("path %q escapes workspace root", path)
	}

	return resolved, nil
}

// resolveWithAncestors resolves an absolute path by walking up the directory
// tree to find the nearest existing ancestor, resolving its symlinks, then
// reconstructing the full path. This handles cases where intermediate
// directories don't exist (e.g. new files, path traversal to non-existent dirs).
func resolveWithAncestors(abs string) (string, error) {
	// Fast path: target exists
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("path resolution failed: %w", err)
	}

	// Walk up to find nearest existing ancestor
	current := abs
	var tail []string
	for {
		parent := filepath.Dir(current)
		tail = append(tail, filepath.Base(current))
		if parent == current {
			// Reached filesystem root without finding existing dir
			return "", fmt.Errorf("no accessible ancestor for path %q", abs)
		}
		resolved, err = filepath.EvalSymlinks(parent)
		if err == nil {
			// Found existing ancestor — reconstruct path
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("path resolution failed: %w", err)
		}
		current = parent
	}
}

// isUnderRoot checks if path is equal to or a descendant of root.
func isUnderRoot(path, root string) bool {
	if path == root {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}
