package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultGlobMaxResults = 200
)

func globTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "glob",
		description: "Find files matching a glob pattern. Supports ** for recursive directory matching.",
		schema:      globSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return executeGlob(ctx, rootDir, req)
		},
	}
}

func executeGlob(ctx context.Context, rootDir string, req ToolRequest) (*ToolResponse, error) {
	pattern, _ := req.Params["pattern"].(string)
	if pattern == "" {
		return errorResponse("missing required parameter: pattern"), nil
	}

	maxResults := intParam(req.Params, "max_results", defaultGlobMaxResults)
	if maxResults <= 0 {
		maxResults = defaultGlobMaxResults
	}

	resolvedRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return errorResponse(fmt.Sprintf("root dir not accessible: %v", err)), nil
	}

	var matches []string
	truncated := false

	walkErr := filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Skip hidden directories
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(resolvedRoot, path)

		if globMatch(pattern, relPath) {
			if len(matches) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}
			matches = append(matches, relPath)
		}
		return nil
	})

	if walkErr != nil && ctx.Err() == nil {
		return errorResponse(fmt.Sprintf("glob error: %v", walkErr)), nil
	}

	if len(matches) == 0 {
		return textResponse("no files matched"), nil
	}

	// Deterministic sorted output
	sort.Strings(matches)

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d file(s)", len(matches))
	if truncated {
		fmt.Fprintf(&b, " (truncated at %d)", maxResults)
	}
	b.WriteString("\n")

	for _, m := range matches {
		b.WriteString(m)
		b.WriteString("\n")
	}

	return textResponse(b.String()), nil
}

// globMatch matches a path against a doublestar-style pattern.
func globMatch(pattern, path string) bool {
	patParts := splitPath(pattern)
	pathParts := splitPath(path)
	return matchParts(patParts, pathParts)
}

func splitPath(p string) []string {
	p = filepath.ToSlash(p)
	parts := strings.Split(p, "/")
	var result []string
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func matchParts(pattern, path []string) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			pattern = pattern[1:]
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(path); i++ {
				if matchParts(pattern, path[i:]) {
					return true
				}
			}
			return false
		}

		if len(path) == 0 {
			return false
		}

		matched, _ := filepath.Match(pattern[0], path[0])
		if !matched {
			return false
		}

		pattern = pattern[1:]
		path = path[1:]
	}

	return len(path) == 0
}

var globSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "Glob pattern to match files. Use ** for recursive directory matching (e.g. 'src/**/*.go')"
		},
		"max_results": {
			"type": "integer",
			"description": "Maximum number of files to return (default 200)"
		}
	},
	"required": ["pattern"]
}`)
