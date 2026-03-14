package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/sandbox/gvisor"
	"h2-agent-runtime/internal/tools"
)

func (svc *SandboxHostService) ExecuteTool(ctx context.Context, req ExecuteToolRequest) (*ExecuteToolResponse, error) {
	sess, err := svc.getActiveSession(req.SessionID)
	if err != nil {
		return nil, err
	}

	sess.mu.Lock()
	if sess.state != SessionActive {
		sess.mu.Unlock()
		return nil, fmt.Errorf("%w: session is %s", ErrInvalidState, sess.state)
	}
	if sess.rollingBack {
		sess.mu.Unlock()
		return nil, ErrRollbackInProgress
	}
	sess.activeTools.Add(1)
	mountpoint := sess.mountpoint
	sess.mu.Unlock()
	defer sess.activeTools.Add(-1)

	if svc.config.ToolTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, svc.config.ToolTimeout)
		defer cancel()
	}

	tier := tools.ClassifyTool(req.ToolName)
	start := time.Now()
	var resp *ExecuteToolResponse
	switch tier {
	case tools.Tier1:
		resp, err = svc.executeTier1(ctx, mountpoint, req)
	case tools.Tier2:
		resp, err = svc.executeTier2(ctx, mountpoint, req)
	default:
		err = fmt.Errorf("unknown tier")
	}
	if err != nil {
		return nil, err
	}
	svc.metrics.toolExecutions.Add(1)
	resp.Duration = time.Since(start)
	resp.Tier = int(tier)

	if svc.config.PerToolSnapshots && req.ToolCallID != "" {
		snapName := fmt.Sprintf("tool-%s-%s", req.ToolCallID, time.Now().Format("20060102-150405"))
		if snap, snapErr := svc.zfs.CreateSnapshot(ctx, sess.dataset, snapName); snapErr == nil {
			resp.SnapshotID = snap.Name
			sess.mu.Lock()
			sess.snapshots = append(sess.snapshots, SnapshotEntry{Name: snap.Name, IsTurnSnapshot: false})
			sess.mu.Unlock()
		}
	}
	return resp, nil
}

func (svc *SandboxHostService) executeTier1(ctx context.Context, mountpoint string, req ExecuteToolRequest) (*ExecuteToolResponse, error) {
	if err := validatePathParams(mountpoint, req.Params); err != nil {
		return nil, err
	}
	backend := tools.NewLocalBackend(mountpoint)
	resp, err := backend.ExecuteTool(ctx, tools.ToolRequest{ToolName: req.ToolName, ToolCallID: req.ToolCallID, Params: req.Params}, func(progress tools.ToolProgress) {
		if req.OnProgress != nil {
			req.OnProgress(progress.Content, progress.IsError)
		}
	})
	if err != nil {
		return nil, err
	}
	return &ExecuteToolResponse{
		Content:       blocksToText(resp.Content),
		ContentBlocks: append([]ai.ContentBlock(nil), resp.Content...),
		ExitCode:      resp.ExitCode,
		SnapshotID:    resp.SnapshotID,
	}, nil
}

func (svc *SandboxHostService) executeTier2(ctx context.Context, mountpoint string, req ExecuteToolRequest) (*ExecuteToolResponse, error) {
	resources := svc.config.DefaultResources
	if req.Resources != nil {
		resources = *req.Resources
	}
	cmd, err := buildTier2Command(req.ToolName, req.Params)
	if err != nil {
		return nil, err
	}
	opts := gvisor.ContainerOptions{
		Command:   cmd,
		WorkDir:   "/workspace",
		RootFS:    mountpoint,
		Resources: resources,
		Network:   gvisor.NetworkNone,
	}
	if req.OnProgress != nil {
		opts.StdoutWriter = &progressWriter{onChunk: func(chunk string) { req.OnProgress(chunk, false) }}
		opts.StderrWriter = &progressWriter{onChunk: func(chunk string) { req.OnProgress(chunk, true) }}
	}

	result, err := svc.gvisor.Run(ctx, opts)
	if err != nil {
		return nil, err
	}
	exitCode := result.ExitCode
	content := strings.TrimSpace(string(result.Stdout))
	if stderr := strings.TrimSpace(string(result.Stderr)); stderr != "" {
		if content != "" {
			content += "\n"
		}
		content += stderr
	}
	return &ExecuteToolResponse{
		Content:       content,
		ContentBlocks: []ai.ContentBlock{&ai.TextContent{Text: content}},
		ExitCode:      &exitCode,
	}, nil
}

func buildTier2Command(toolName string, params map[string]any) ([]string, error) {
	switch toolName {
	case "bash":
		cmd, _ := params["cmd"].(string)
		if strings.TrimSpace(cmd) == "" {
			return nil, fmt.Errorf("bash cmd is required")
		}
		return []string{"/bin/bash", "-c", cmd}, nil
	case "git_add":
		path, _ := params["path"].(string)
		if path == "" {
			path = "."
		}
		return []string{"git", "add", path}, nil
	case "git_commit":
		msg, _ := params["message"].(string)
		if strings.TrimSpace(msg) == "" {
			return nil, fmt.Errorf("git_commit message is required")
		}
		return []string{"git", "commit", "-m", msg}, nil
	case "git_push", "git_clone", "git_fetch", "git_pull":
		args, _ := params["args"].([]any)
		cmd := []string{"git", strings.TrimPrefix(toolName, "git_")}
		for _, a := range args {
			if s, ok := a.(string); ok && s != "" {
				cmd = append(cmd, s)
			}
		}
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown Tier 2 tool: %s", toolName)
	}
}

func validatePathParams(rootDir string, params map[string]any) error {
	for key, val := range params {
		if key != "path" && key != "file_path" && key != "directory" {
			continue
		}
		p, ok := val.(string)
		if !ok {
			continue
		}
		if err := validatePath(rootDir, p); err != nil {
			return err
		}
	}
	return nil
}

func validatePath(rootDir, requested string) error {
	abs := requested
	if !filepath.IsAbs(requested) {
		abs = filepath.Join(rootDir, requested)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(rootDir, abs)
	if err != nil {
		return fmt.Errorf("path escape: cannot compute relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path escape: %q resolves outside session root", requested)
	}
	return nil
}

func blocksToText(blocks []ai.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case *ai.TextContent:
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		case *ai.ThinkingContent:
			if b.Thinking != "" {
				parts = append(parts, b.Thinking)
			}
		case *ai.ToolCall:
			encoded, _ := json.Marshal(b.Arguments)
			parts = append(parts, fmt.Sprintf("tool_call:%s %s", b.Name, string(encoded)))
		case *ai.ImageContent:
			parts = append(parts, fmt.Sprintf("image:%s bytes=%d", b.MimeType, len(b.Data)))
		default:
			parts = append(parts, fmt.Sprintf("%T", b))
		}
	}
	return strings.Join(parts, "\n")
}

type progressWriter struct {
	onChunk func(chunk string)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	if w.onChunk == nil || len(p) == 0 {
		return len(p), nil
	}
	w.onChunk(string(p))
	return len(p), nil
}

var _ io.Writer = (*progressWriter)(nil)
