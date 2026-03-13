package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func editFileTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "edit_file",
		description: "Edit a file by replacing an exact string match. Requires old_string (text to find) and new_string (replacement). By default replaces only the first occurrence; set replace_all=true to replace all.",
		schema:      editFileSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return executeEditFile(ctx, rootDir, req)
		},
	}
}

func executeEditFile(_ context.Context, rootDir string, req ToolRequest) (*ToolResponse, error) {
	path, _ := req.Params["path"].(string)
	if path == "" {
		return errorResponse("missing required parameter: path"), nil
	}
	oldStr, _ := req.Params["old_string"].(string)
	if oldStr == "" {
		return errorResponse("missing required parameter: old_string"), nil
	}
	newStr, _ := req.Params["new_string"].(string)
	replaceAll, _ := req.Params["replace_all"].(bool)

	resolved, err := resolveSafePath(rootDir, path)
	if err != nil {
		return errorResponse(err.Error()), nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResponse(fmt.Sprintf("file not found: %s", path)), nil
		}
		return errorResponse(fmt.Sprintf("read error: %v", err)), nil
	}

	content := string(data)
	count := strings.Count(content, oldStr)

	if count == 0 {
		return mismatchResponse(path, oldStr, content), nil
	}

	if !replaceAll && count > 1 {
		return errorResponse(fmt.Sprintf(
			"old_string found %d times in %s — provide more context to uniquely identify the target, or set replace_all=true",
			count, path)), nil
	}

	var result string
	if replaceAll {
		result = strings.ReplaceAll(content, oldStr, newStr)
	} else {
		result = strings.Replace(content, oldStr, newStr, 1)
	}

	if err := atomicWriteFile(resolved, []byte(result), 0o644); err != nil {
		return errorResponse(fmt.Sprintf("write error: %v", err)), nil
	}

	replacements := 1
	if replaceAll {
		replacements = count
	}
	return textResponse(fmt.Sprintf("edited %s: %d replacement(s) made", path, replacements)), nil
}

// mismatchResponse builds a structured diagnostic when old_string isn't found.
func mismatchResponse(path, oldStr, content string) *ToolResponse {
	msg := fmt.Sprintf("old_string not found in %s", path)

	lines := strings.Split(content, "\n")
	if len(lines) <= 20 {
		msg += fmt.Sprintf("\n\nFile has %d lines. Full content:\n%s", len(lines), content)
	} else {
		firstLine := strings.Split(oldStr, "\n")[0]
		if firstLine != "" {
			for i, line := range lines {
				if strings.Contains(line, strings.TrimSpace(firstLine)) {
					start := i
					if start > 2 {
						start = i - 2
					}
					end := i + 5
					if end > len(lines) {
						end = len(lines)
					}
					msg += fmt.Sprintf("\n\nNearest partial match around line %d:\n", i+1)
					for j := start; j < end; j++ {
						msg += fmt.Sprintf("%6d\t%s\n", j+1, lines[j])
					}
					break
				}
			}
		}
	}

	return errorResponse(msg)
}

var editFileSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "The path to the file to edit"
		},
		"old_string": {
			"type": "string",
			"description": "The exact text to find and replace"
		},
		"new_string": {
			"type": "string",
			"description": "The replacement text"
		},
		"replace_all": {
			"type": "boolean",
			"description": "Replace all occurrences instead of just the first"
		}
	},
	"required": ["path", "old_string", "new_string"]
}`)
