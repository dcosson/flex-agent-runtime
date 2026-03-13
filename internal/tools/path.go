package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveSafePath canonicalizes path and ensures it stays within rootDir.
// Follows symlinks during resolution to prevent symlink-based escapes.
// Handles non-existent paths by resolving the nearest existing ancestor.
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

	// Resolve the real path, walking up to find an existing ancestor
	resolved, err := resolveWithAncestors(abs)
	if err != nil {
		return "", fmt.Errorf("path resolution failed: %w", err)
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

// resolveWithAncestors resolves symlinks for a path that may not fully exist.
// It walks up the path tree until it finds an existing ancestor, resolves
// symlinks there, then appends the remaining path components.
func resolveWithAncestors(abs string) (string, error) {
	// Try direct resolution first
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	// Walk up to find nearest existing ancestor
	current := abs
	var tail []string
	for {
		parent := filepath.Dir(current)
		tail = append([]string{filepath.Base(current)}, tail...)
		if parent == current {
			// Reached filesystem root without finding existing dir
			break
		}
		current = parent

		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			// Found existing ancestor — reconstruct full path
			for _, part := range tail {
				resolved = filepath.Join(resolved, part)
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}

	// If we got here, no ancestor exists — just return the cleaned path
	return abs, nil
}

// isUnderRoot checks if path is equal to or a descendant of root.
func isUnderRoot(path, root string) bool {
	if path == root {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}
