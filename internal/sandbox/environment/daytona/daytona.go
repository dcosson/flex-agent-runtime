package daytona

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/restapi"
)

const (
	defaultBaseURL = "https://app.daytona.io/api/v1"
)

type Option func(*DaytonaSandboxEnvironment)

func WithBaseURL(baseURL string) Option {
	return func(e *DaytonaSandboxEnvironment) {
		if strings.TrimSpace(baseURL) != "" {
			e.api.BaseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(e *DaytonaSandboxEnvironment) {
		if client != nil {
			e.api.HTTPClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(e *DaytonaSandboxEnvironment) {
		if logger != nil {
			e.logger = logger
		}
	}
}

// DaytonaOptions carries Daytona-specific session create parameters.
type DaytonaOptions struct {
	Target  string            // Daytona target (e.g., "local", "aws")
	Image   string            // container image
	GitURL  string            // repository to clone
	EnvVars map[string]string // environment variables
}

// DaytonaSandboxEnvironment implements environment.ExecutionEnvironment using the Daytona API.
//
// Key mapping:
//
//	Create      → workspace.create(target, source)
//	ExecuteTool → workspace.code_run() or workspace.filesystem operations
//	Pause       → ErrCapabilityNotSupported (auto-stop is lossy)
//	Resume      → ErrCapabilityNotSupported
//	Destroy     → workspace.delete()
type DaytonaSandboxEnvironment struct {
	api    restapi.Client
	logger *slog.Logger

	mu          sync.RWMutex
	workspaceID string
	state       environment.SessionState
	created     time.Time
	labels      map[string]string
}

func NewDaytonaSandboxEnvironment(apiKey string, opts ...Option) *DaytonaSandboxEnvironment {
	e := &DaytonaSandboxEnvironment{
		api: restapi.Client{
			BaseURL:    defaultBaseURL,
			AuthToken:  strings.TrimSpace(apiKey),
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		},
		logger: slog.Default(),
		state:  environment.StateCreating,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *DaytonaSandboxEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
	if config.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	var opts DaytonaOptions
	if config.Options != nil {
		casted, ok := config.Options.(DaytonaOptions)
		if !ok {
			return fmt.Errorf("daytona: Options must be daytona.DaytonaOptions, got %T", config.Options)
		}
		opts = casted
	}

	image := opts.Image
	if image == "" {
		image = strings.TrimSpace(config.BaseImage)
	}
	if image == "" {
		return fmt.Errorf("daytona: image is required (set BaseImage or DaytonaOptions.Image)")
	}

	payload := map[string]any{
		"name":   config.SessionID,
		"image":  image,
		"labels": config.Labels,
	}
	if opts.Target != "" {
		payload["target"] = opts.Target
	}
	if opts.GitURL != "" {
		payload["source"] = map[string]any{"repository": opts.GitURL}
	}
	if len(opts.EnvVars) > 0 {
		payload["env_vars"] = opts.EnvVars
	}

	var resp struct {
		ID string `json:"id"`
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, "/workspaces", payload, &resp); err != nil {
		return fmt.Errorf("daytona: create workspace: %w", err)
	}
	if resp.ID == "" {
		return fmt.Errorf("daytona: create workspace: missing workspace id")
	}

	e.mu.Lock()
	e.workspaceID = resp.ID
	e.state = environment.StateActive
	e.created = time.Now()
	e.labels = restapi.CloneLabels(config.Labels)
	e.mu.Unlock()

	return nil
}

func (e *DaytonaSandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	workspaceID, err := e.activeWorkspaceID()
	if err != nil {
		return nil, err
	}

	if environment.IsFileOp(req.ToolName) {
		return e.executeFileOp(ctx, workspaceID, req)
	}
	return e.executeCommand(ctx, workspaceID, req, onProgress)
}

func (e *DaytonaSandboxEnvironment) Pause(context.Context) error {
	return environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) Resume(context.Context) error {
	return environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) Destroy(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state == environment.StateDestroyed {
		return nil
	}
	if e.workspaceID == "" {
		return environment.ErrNotActive
	}
	if err := e.api.DoJSON(ctx, http.MethodDelete, "/workspaces/"+e.workspaceID, nil, nil); err != nil {
		return fmt.Errorf("daytona: destroy workspace: %w", err)
	}
	e.state = environment.StateDestroyed
	return nil
}

func (e *DaytonaSandboxEnvironment) State() environment.SessionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

func (e *DaytonaSandboxEnvironment) Capabilities() environment.Capabilities {
	return environment.DaytonaCapabilities
}

func (e *DaytonaSandboxEnvironment) CreateSnapshot(context.Context, string) (*environment.SnapshotInfo, error) {
	return nil, environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) Rollback(context.Context, string) error {
	return environment.ErrCapabilityNotSupported
}

func (e *DaytonaSandboxEnvironment) activeWorkspaceID() (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state != environment.StateActive || e.workspaceID == "" {
		return "", environment.ErrNotActive
	}
	return e.workspaceID, nil
}

func (e *DaytonaSandboxEnvironment) executeFileOp(ctx context.Context, workspaceID string, req environment.ToolRequest) (*environment.ToolResponse, error) {
	payload := map[string]any{
		"tool_name": req.ToolName,
		"params":    req.Params,
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, "/workspaces/"+workspaceID+"/filesystem/"+req.ToolName, payload, &resp); err != nil {
		return nil, fmt.Errorf("daytona: execute file op %s: %w", req.ToolName, err)
	}
	return &environment.ToolResponse{
		Content: []ai.ContentBlock{&ai.TextContent{Text: resp.Content}},
	}, nil
}

func (e *DaytonaSandboxEnvironment) executeCommand(ctx context.Context, workspaceID string, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	payload := map[string]any{
		"tool_name": req.ToolName,
		"params":    req.Params,
	}
	var resp struct {
		Content  string `json:"content"`
		ExitCode *int   `json:"exit_code,omitempty"`
		Progress []struct {
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		} `json:"progress,omitempty"`
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, "/workspaces/"+workspaceID+"/commands/run", payload, &resp); err != nil {
		return nil, fmt.Errorf("daytona: execute command %s: %w", req.ToolName, err)
	}
	if onProgress != nil {
		for _, p := range resp.Progress {
			onProgress(environment.ToolProgress{Content: p.Content, IsError: p.IsError})
		}
	}
	return &environment.ToolResponse{
		Content:  []ai.ContentBlock{&ai.TextContent{Text: resp.Content}},
		ExitCode: resp.ExitCode,
	}, nil
}
