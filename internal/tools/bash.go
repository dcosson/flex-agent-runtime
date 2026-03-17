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
	defaultBashTimeout = 120 * time.Second
	maxBashOutput      = 256 * 1024 // 256 KB
)

func bashTool(rootDir string) toolImpl {
	return toolImpl{
		name:        "bash",
		description: "Execute a shell command in the workspace directory. Returns stdout, stderr, and exit code.",
		schema:      bashSchema,
		execute: func(ctx context.Context, req ToolRequest) (*ToolResponse, error) {
			return executeBash(ctx, rootDir, req)
		},
	}
}

func executeBash(ctx context.Context, rootDir string, req ToolRequest) (*ToolResponse, error) {
	cmdStr, _ := req.Params["cmd"].(string)
	if cmdStr == "" {
		return errorResponse("missing required parameter: cmd"), nil
	}

	timeoutMs := intParam(req.Params, "timeout_ms", 0)
	timeout := defaultBashTimeout
	if timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", cmdStr)
	cmd.Dir = rootDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if cmdCtx.Err() == context.DeadlineExceeded {
			exitCode = 124
		} else {
			return errorResponse(fmt.Sprintf("command execution failed: %v", err)), nil
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	stdoutTrunc := false
	stderrTrunc := false
	if len(stdoutStr) > maxBashOutput {
		stdoutStr = stdoutStr[:maxBashOutput]
		stdoutTrunc = true
	}
	if len(stderrStr) > maxBashOutput {
		stderrStr = stderrStr[:maxBashOutput]
		stderrTrunc = true
	}

	var b strings.Builder
	if stdoutStr != "" {
		b.WriteString(stdoutStr)
		if stdoutTrunc {
			b.WriteString("\n... (stdout truncated)")
		}
	}
	if stderrStr != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("STDERR:\n")
		b.WriteString(stderrStr)
		if stderrTrunc {
			b.WriteString("\n... (stderr truncated)")
		}
	}

	if cmdCtx.Err() == context.DeadlineExceeded {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "(timed out after %s)", timeout)
	}

	if b.Len() == 0 {
		b.WriteString("(no output)")
	}

	fmt.Fprintf(&b, "\n\nExit code: %d\nDuration: %s", exitCode, duration.Round(time.Millisecond))

	ec := exitCode
	return &ToolResponse{
		Content:  []ai.ContentBlock{&ai.TextContent{Text: b.String()}},
		ExitCode: &ec,
	}, nil
}

var bashSchema = mustSchema(`{
	"type": "object",
	"properties": {
		"cmd": {
			"type": "string",
			"description": "The shell command to execute"
		},
		"timeout_ms": {
			"type": "integer",
			"description": "Timeout in milliseconds (default 120000)"
		}
	},
	"required": ["cmd"]
}`)
