package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// FileExpectation defines expected file state after a scenario.
type FileExpectation struct {
	Path     string // relative to workspace root
	Contains string // substring that must appear in file content
	Equals   string // exact match (if non-empty, overrides Contains)
	Absent   bool   // file must NOT exist
}

// AssertFiles verifies file expectations against the workspace.
func AssertFiles(t *testing.T, root string, expectations []FileExpectation) {
	t.Helper()
	for _, exp := range expectations {
		path := filepath.Join(root, exp.Path)
		data, err := os.ReadFile(path)

		if exp.Absent {
			if err == nil {
				t.Errorf("expected %s to be absent, but it exists", exp.Path)
			}
			continue
		}

		if err != nil {
			t.Errorf("expected %s to exist: %v", exp.Path, err)
			continue
		}

		content := string(data)
		if exp.Equals != "" {
			if content != exp.Equals {
				t.Errorf("file %s content mismatch:\ngot:  %q\nwant: %q", exp.Path, content, exp.Equals)
			}
		} else if exp.Contains != "" {
			if !strings.Contains(content, exp.Contains) {
				t.Errorf("file %s does not contain %q\ncontent: %q", exp.Path, exp.Contains, content)
			}
		}
	}
}

// WorkspaceSnapshot captures all files in a directory tree.
type WorkspaceSnapshot struct {
	Files map[string]string // relative path -> content
}

// SnapshotWorkspace captures the current state of a workspace.
func SnapshotWorkspace(root string) (*WorkspaceSnapshot, error) {
	snap := &WorkspaceSnapshot{Files: make(map[string]string)}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snap.Files[rel] = string(data)
		return nil
	})
	return snap, err
}

// Diff returns the changes between two snapshots.
func (s *WorkspaceSnapshot) Diff(other *WorkspaceSnapshot) string {
	var diffs []string
	allPaths := make(map[string]bool)
	for p := range s.Files {
		allPaths[p] = true
	}
	for p := range other.Files {
		allPaths[p] = true
	}

	sorted := make([]string, 0, len(allPaths))
	for p := range allPaths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)

	for _, p := range sorted {
		oldContent, hadOld := s.Files[p]
		newContent, hadNew := other.Files[p]

		if !hadOld && hadNew {
			diffs = append(diffs, fmt.Sprintf("+ %s (created)", p))
		} else if hadOld && !hadNew {
			diffs = append(diffs, fmt.Sprintf("- %s (deleted)", p))
		} else if oldContent != newContent {
			diffs = append(diffs, fmt.Sprintf("~ %s (modified)", p))
		}
	}
	return strings.Join(diffs, "\n")
}
