package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

const (
	defaultReadLimit = 2000             // default max lines to return
	maxReadFileSize  = 10 * 1024 * 1024 // 10 MB max file size
	maxLineLength    = 2000             // truncate lines longer than this
)

func readFileTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "read_file",
		description: "Read a file from the filesystem. Returns file content with line numbers.",
		schema:      readFileSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return executeReadFile(ctx, rootDir, req)
		},
	}
}

func executeReadFile(_ context.Context, rootDir string, req ToolRequest) (*ToolResponse, error) {
	path, _ := req.Params["path"].(string)
	if path == "" {
		return errorResponse("missing required parameter: path"), nil
	}

	resolved, err := resolveSafePath(rootDir, path)
	if err != nil {
		return errorResponse(err.Error()), nil
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return errorResponse(fmt.Sprintf("file not found: %s", path)), nil
		}
		return errorResponse(fmt.Sprintf("cannot access file: %v", err)), nil
	}
	if info.IsDir() {
		return errorResponse(fmt.Sprintf("%s is a directory, not a file", path)), nil
	}
	if info.Size() > maxReadFileSize {
		return errorResponse(fmt.Sprintf("file too large: %d bytes (max %d)", info.Size(), maxReadFileSize)), nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return errorResponse(fmt.Sprintf("read error: %v", err)), nil
	}

	offset := intParam(req.Params, "offset", 0)
	limit := intParam(req.Params, "limit", defaultReadLimit)
	if limit <= 0 {
		limit = defaultReadLimit
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	if offset < 0 {
		offset = 0
	}
	if offset >= totalLines {
		return textResponse(fmt.Sprintf("offset %d beyond end of file (%d lines)", offset, totalLines)), nil
	}

	end := offset + limit
	truncated := false
	if end > totalLines {
		end = totalLines
	} else if end < totalLines {
		truncated = true
	}

	var b strings.Builder
	for i := offset; i < end; i++ {
		line := lines[i]
		if len(line) > maxLineLength {
			line = line[:maxLineLength] + "... (truncated)"
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}

	if truncated {
		fmt.Fprintf(&b, "\n(showing lines %d-%d of %d total)", offset+1, end, totalLines)
	}

	return textResponse(b.String()), nil
}

var readFileSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "The path to the file to read"
		},
		"offset": {
			"type": "integer",
			"description": "Line number to start reading from (0-based)"
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of lines to read"
		}
	},
	"required": ["path"]
}`)

// intParam extracts an integer from params with a default.
func intParam(params map[string]any, key string, defaultVal int) int {
	v, ok := params[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return defaultVal
	}
}

// errorResponse builds a ToolResponse with an error text block.
func errorResponse(msg string) *ToolResponse {
	return &ToolResponse{
		Content: []ai.ContentBlock{&ai.TextContent{Text: msg}},
	}
}

// textResponse builds a ToolResponse with a text content block.
func textResponse(text string) *ToolResponse {
	return &ToolResponse{
		Content: []ai.ContentBlock{&ai.TextContent{Text: text}},
	}
}
