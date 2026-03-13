package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultGrepMaxResults   = 100
	defaultGrepContextLines = 0
	maxGrepMatchLength      = 500
)

func grepTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "grep",
		description: "Search file contents using regex or literal patterns. Returns matching lines with file paths and line numbers.",
		schema:      grepSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return executeGrep(ctx, rootDir, req)
		},
	}
}

func executeGrep(ctx context.Context, rootDir string, req ToolRequest) (*ToolResponse, error) {
	pattern, _ := req.Params["pattern"].(string)
	if pattern == "" {
		return errorResponse("missing required parameter: pattern"), nil
	}

	include, _ := req.Params["include"].(string)
	exclude, _ := req.Params["exclude"].(string)
	literal, _ := req.Params["literal"].(bool)
	maxResults := intParam(req.Params, "max_results", defaultGrepMaxResults)
	contextLines := intParam(req.Params, "context_lines", defaultGrepContextLines)

	if maxResults <= 0 {
		maxResults = defaultGrepMaxResults
	}
	if contextLines < 0 {
		contextLines = 0
	}

	// Compile pattern
	var re *regexp.Regexp
	var literalStr string
	if literal {
		literalStr = pattern
	} else {
		var err error
		re, err = regexp.Compile(pattern)
		if err != nil {
			return errorResponse(fmt.Sprintf("invalid regex pattern: %v", err)), nil
		}
	}

	resolvedRoot, err := filepath.EvalSymlinks(rootDir)
	if err != nil {
		return errorResponse(fmt.Sprintf("root dir not accessible: %v", err)), nil
	}

	type matchRecord struct {
		file    string
		line    int
		content string
		context []string
	}

	var matches []matchRecord
	truncated := false

	walkErr := filepath.WalkDir(resolvedRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
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

		// Skip non-regular files
		if !d.Type().IsRegular() {
			return nil
		}

		relPath, _ := filepath.Rel(resolvedRoot, path)

		// Apply include/exclude filters
		if include != "" {
			matched, _ := filepath.Match(include, filepath.Base(path))
			if !matched {
				return nil
			}
		}
		if exclude != "" {
			matched, _ := filepath.Match(exclude, filepath.Base(path))
			if matched {
				return nil
			}
		}

		// Read file
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable
		}

		// Skip binary files (simple heuristic)
		if isBinary(data) {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if len(matches) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}

			var found bool
			if literal {
				found = strings.Contains(line, literalStr)
			} else {
				found = re.MatchString(line)
			}

			if found {
				content := line
				if len(content) > maxGrepMatchLength {
					content = content[:maxGrepMatchLength] + "..."
				}

				rec := matchRecord{
					file:    relPath,
					line:    i + 1,
					content: content,
				}

				// Add context lines if requested
				if contextLines > 0 {
					start := i - contextLines
					if start < 0 {
						start = 0
					}
					end := i + contextLines + 1
					if end > len(lines) {
						end = len(lines)
					}
					for j := start; j < end; j++ {
						if j == i {
							continue
						}
						cl := lines[j]
						if len(cl) > maxGrepMatchLength {
							cl = cl[:maxGrepMatchLength] + "..."
						}
						rec.context = append(rec.context, fmt.Sprintf("%d: %s", j+1, cl))
					}
				}

				matches = append(matches, rec)
			}
		}
		return nil
	})

	if walkErr != nil && ctx.Err() == nil {
		return errorResponse(fmt.Sprintf("search error: %v", walkErr)), nil
	}

	if len(matches) == 0 {
		return textResponse("no matches found"), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d match(es)", len(matches))
	if truncated {
		fmt.Fprintf(&b, " (truncated at %d)", maxResults)
	}
	b.WriteString("\n\n")

	for _, m := range matches {
		fmt.Fprintf(&b, "%s:%d: %s\n", m.file, m.line, m.content)
		for _, cl := range m.context {
			fmt.Fprintf(&b, "  %s\n", cl)
		}
	}

	return textResponse(b.String()), nil
}

// isBinary checks if data appears to be binary content.
func isBinary(data []byte) bool {
	check := data
	if len(check) > 8192 {
		check = check[:8192]
	}
	for _, b := range check {
		if b == 0 {
			return true
		}
	}
	return false
}

var grepSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "The regex pattern (or literal string) to search for"
		},
		"include": {
			"type": "string",
			"description": "Glob pattern to include only matching filenames (e.g. '*.go')"
		},
		"exclude": {
			"type": "string",
			"description": "Glob pattern to exclude matching filenames"
		},
		"literal": {
			"type": "boolean",
			"description": "If true, treat pattern as literal string instead of regex"
		},
		"max_results": {
			"type": "integer",
			"description": "Maximum number of matching lines to return (default 100)"
		},
		"context_lines": {
			"type": "integer",
			"description": "Number of context lines to show before and after each match"
		}
	},
	"required": ["pattern"]
}`)
