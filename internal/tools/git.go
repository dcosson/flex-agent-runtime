package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

const (
	defaultGitTimeout = 30 * time.Second
	maxGitOutput      = 256 * 1024 // 256 KB
)

func gitStatusTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "git_status",
		description: "Show the working tree status. Returns modified, staged, and untracked files.",
		schema:      gitStatusSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return runGitCommand(ctx, rootDir, []string{"status", "--porcelain=v1"})
		},
	}
}

func gitDiffTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "git_diff",
		description: "Show changes between commits, working tree, etc.",
		schema:      gitDiffSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			args := []string{"diff"}
			if ref, _ := req.Params["ref"].(string); ref != "" {
				args = append(args, ref)
			}
			if staged, _ := req.Params["staged"].(bool); staged {
				args = append(args, "--cached")
			}
			if path, _ := req.Params["path"].(string); path != "" {
				args = append(args, "--", path)
			}
			return runGitCommand(ctx, rootDir, args)
		},
	}
}

func gitLogTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "git_log",
		description: "Show commit log. Returns recent commits with hash, author, date, and message.",
		schema:      gitLogSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			n := intParam(req.Params, "n", 10)
			if n <= 0 {
				n = 10
			}
			if n > 100 {
				n = 100
			}
			args := []string{"log", fmt.Sprintf("-n%d", n), "--format=%H %an %ai %s"}
			if ref, _ := req.Params["ref"].(string); ref != "" {
				args = append(args, ref)
			}
			if path, _ := req.Params["path"].(string); path != "" {
				args = append(args, "--", path)
			}
			return runGitCommand(ctx, rootDir, args)
		},
	}
}

func gitShowTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "git_show",
		description: "Show a commit, tag, or other git object.",
		schema:      gitShowSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			ref, _ := req.Params["ref"].(string)
			if ref == "" {
				ref = "HEAD"
			}
			return runGitCommand(ctx, rootDir, []string{"show", ref})
		},
	}
}

func gitAddTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "git_add",
		description: "Add file contents to the staging area.",
		schema:      gitAddSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			paths, _ := req.Params["paths"].([]any)
			if len(paths) == 0 {
				return errorResponse("missing required parameter: paths"), nil
			}

			args := []string{"add"}
			for _, p := range paths {
				if s, ok := p.(string); ok && s != "" {
					// Validate each path is within workspace
					_, err := resolveSafePath(rootDir, s)
					if err != nil {
						return errorResponse(fmt.Sprintf("path %q: %v", s, err)), nil
					}
					args = append(args, s)
				}
			}

			if len(args) == 1 {
				return errorResponse("no valid paths provided"), nil
			}

			return runGitCommand(ctx, rootDir, args)
		},
	}
}

func gitCommitTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "git_commit",
		description: "Record changes to the repository.",
		schema:      gitCommitSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			message, _ := req.Params["message"].(string)
			if message == "" {
				return errorResponse("missing required parameter: message"), nil
			}

			args := []string{"commit", "-m", message}
			return runGitCommand(ctx, rootDir, args)
		},
	}
}

func runGitCommand(ctx context.Context, rootDir string, args []string) (*ToolResponse, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultGitTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd.Dir = rootDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	// Truncate
	if len(stdoutStr) > maxGitOutput {
		stdoutStr = stdoutStr[:maxGitOutput] + "\n... (output truncated)"
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if cmdCtx.Err() == context.DeadlineExceeded {
			return errorResponse("git command timed out"), nil
		} else {
			return errorResponse(fmt.Sprintf("git command failed: %v", err)), nil
		}
	}

	var b strings.Builder
	if stdoutStr != "" {
		b.WriteString(stdoutStr)
	}
	if stderrStr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(stderrStr)
	}
	if b.Len() == 0 {
		b.WriteString("(no output)")
	}

	ec := exitCode
	return &ToolResponse{
		Content:  []ai.ContentBlock{&ai.TextContent{Text: b.String()}},
		ExitCode: &ec,
	}, nil
}

// --- Schemas ---

var gitStatusSchema = mustSchema(`{
	"type": "object",
	"properties": {},
	"required": []
}`)

var gitDiffSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"ref": {
			"type": "string",
			"description": "Git ref to diff against (e.g. 'HEAD~1', 'main')"
		},
		"staged": {
			"type": "boolean",
			"description": "If true, show staged changes (--cached)"
		},
		"path": {
			"type": "string",
			"description": "Limit diff to a specific file path"
		}
	},
	"required": []
}`)

var gitLogSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"n": {
			"type": "integer",
			"description": "Number of commits to show (default 10, max 100)"
		},
		"ref": {
			"type": "string",
			"description": "Starting ref (default HEAD)"
		},
		"path": {
			"type": "string",
			"description": "Limit log to a specific file path"
		}
	},
	"required": []
}`)

var gitShowSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"ref": {
			"type": "string",
			"description": "Git ref to show (default HEAD)"
		}
	},
	"required": []
}`)

var gitAddSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"paths": {
			"type": "array",
			"items": {"type": "string"},
			"description": "File paths to stage"
		}
	},
	"required": ["paths"]
}`)

var gitCommitSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"message": {
			"type": "string",
			"description": "Commit message"
		}
	},
	"required": ["message"]
}`)
