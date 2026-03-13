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

	// Resolve symlinks to get the real path
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// If the file doesn't exist yet (write/edit creating new file),
		// resolve the parent directory instead
		if os.IsNotExist(err) {
			dir := filepath.Dir(abs)
			resolvedDir, dirErr := filepath.EvalSymlinks(dir)
			if dirErr != nil {
				return "", fmt.Errorf("path not accessible: %w", dirErr)
			}
			resolved = filepath.Join(resolvedDir, filepath.Base(abs))
		} else {
			return "", fmt.Errorf("path resolution failed: %w", err)
		}
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

// isUnderRoot checks if path is equal to or a descendant of root.
func isUnderRoot(path, root string) bool {
	if path == root {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}
