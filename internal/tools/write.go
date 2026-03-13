package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

func writeFileTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "write_file",
		description: "Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Uses atomic write (temp file + rename) to prevent partial writes.",
		schema:      writeFileSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return executeWriteFile(ctx, rootDir, req)
		},
	}
}

func executeWriteFile(_ context.Context, rootDir string, req ToolRequest) (*ToolResponse, error) {
	path, _ := req.Params["path"].(string)
	if path == "" {
		return errorResponse("missing required parameter: path"), nil
	}
	content, _ := req.Params["content"].(string)

	resolved, err := resolveSafePath(rootDir, path)
	if err != nil {
		return errorResponse(err.Error()), nil
	}

	createDirs, _ := req.Params["create_dirs"].(bool)
	dir := filepath.Dir(resolved)

	if createDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return errorResponse(fmt.Sprintf("cannot create directories: %v", err)), nil
		}
	} else {
		if _, err := os.Stat(dir); err != nil {
			return errorResponse(fmt.Sprintf("directory does not exist: %s (set create_dirs=true to create)", filepath.Dir(path))), nil
		}
	}

	// Atomic write: write to temp file, fsync, rename
	if err := atomicWriteFile(resolved, []byte(content), 0o644); err != nil {
		return errorResponse(fmt.Sprintf("write error: %v", err)), nil
	}

	return textResponse(fmt.Sprintf("wrote %d bytes to %s", len(content), path)), nil
}

// atomicWriteFile writes data to a temp file in the same directory, fsyncs,
// then renames to the target path. This prevents partial writes.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-write-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("fsync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	if err := os.Chmod(tmpName, perm); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	success = true
	return nil
}

var writeFileSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "The path to the file to write"
		},
		"content": {
			"type": "string",
			"description": "The content to write to the file"
		},
		"create_dirs": {
			"type": "boolean",
			"description": "Create parent directories if they don't exist"
		}
	},
	"required": ["path", "content"]
}`)
