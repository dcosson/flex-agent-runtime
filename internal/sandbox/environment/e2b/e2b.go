package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
)

const (
	defaultBaseURL = "https://api.e2b.dev/v1"
	defaultTimeout = 5 * time.Minute
	maxTimeout     = 24 * time.Hour
)

type Option func(*E2BSandboxEnvironment)

func WithBaseURL(baseURL string) Option {
	return func(e *E2BSandboxEnvironment) {
		if strings.TrimSpace(baseURL) != "" {
			e.baseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(e *E2BSandboxEnvironment) {
		if client != nil {
			e.httpClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(e *E2BSandboxEnvironment) {
		if logger != nil {
			e.logger = logger
		}
	}
}

// E2BOptions carries E2B-specific session create parameters.
type E2BOptions struct {
	TemplateID string
	Timeout    time.Duration
	Metadata   map[string]string
}

// E2BSandboxEnvironment implements environment.ExecutionEnvironment using E2B REST APIs.
type E2BSandboxEnvironment struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
	logger     *slog.Logger

	mu        sync.RWMutex
	sandboxID string
	state     environment.SessionState
	created   time.Time
	labels    map[string]string
}

func NewE2BSandboxEnvironment(apiKey string, opts ...Option) *E2BSandboxEnvironment {
	e := &E2BSandboxEnvironment{
		apiKey:     strings.TrimSpace(apiKey),
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    defaultBaseURL,
		logger:     slog.Default(),
		state:      environment.StateCreating,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *E2BSandboxEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
	if config.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	var opts E2BOptions
	if config.Options != nil {
		casted, ok := config.Options.(E2BOptions)
		if !ok {
			return fmt.Errorf("e2b: Options must be e2b.E2BOptions, got %T", config.Options)
		}
		opts = casted
	}

	templateID := opts.TemplateID
	if templateID == "" {
		templateID = strings.TrimSpace(config.BaseImage)
	}
	if templateID == "" {
		return fmt.Errorf("e2b: template_id is required (set BaseImage or E2BOptions.TemplateID)")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	payload := map[string]any{
		"template_id":     templateID,
		"timeout_seconds": int(timeout.Seconds()),
		"metadata":        opts.Metadata,
		"session_id":      config.SessionID,
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := e.doJSON(ctx, http.MethodPost, "/sandboxes", payload, &resp); err != nil {
		return fmt.Errorf("e2b: create sandbox: %w", err)
	}
	if resp.ID == "" {
		return fmt.Errorf("e2b: create sandbox: missing sandbox id")
	}

	e.mu.Lock()
	e.sandboxID = resp.ID
	e.state = environment.StateActive
	e.created = time.Now()
	e.labels = cloneLabels(config.Labels)
	e.mu.Unlock()

	return nil
}

func (e *E2BSandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	sandboxID, err := e.activeSandboxID()
	if err != nil {
		return nil, err
	}

	if environment.IsFileOp(req.ToolName) {
		return e.executeFileOp(ctx, sandboxID, req)
	}
	return e.executeCommand(ctx, sandboxID, req, onProgress)
}

func (e *E2BSandboxEnvironment) Pause(ctx context.Context) error {
	e.mu.RLock()
	state := e.state
	id := e.sandboxID
	e.mu.RUnlock()

	if state != environment.StateActive || id == "" {
		return environment.ErrNotActive
	}
	if err := e.doJSON(ctx, http.MethodPost, "/sandboxes/"+id+"/pause", map[string]any{}, nil); err != nil {
		return fmt.Errorf("e2b: pause: %w", err)
	}

	e.mu.Lock()
	e.state = environment.StatePaused
	e.mu.Unlock()
	return nil
}

func (e *E2BSandboxEnvironment) Resume(ctx context.Context) error {
	e.mu.RLock()
	state := e.state
	id := e.sandboxID
	e.mu.RUnlock()

	if state != environment.StatePaused || id == "" {
		return environment.ErrNotActive
	}
	if err := e.doJSON(ctx, http.MethodPost, "/sandboxes/"+id+"/resume", map[string]any{}, nil); err != nil {
		return fmt.Errorf("e2b: resume: %w", err)
	}

	e.mu.Lock()
	e.state = environment.StateActive
	e.mu.Unlock()
	return nil
}

func (e *E2BSandboxEnvironment) Destroy(ctx context.Context) error {
	e.mu.RLock()
	id := e.sandboxID
	state := e.state
	e.mu.RUnlock()

	if state == environment.StateDestroyed {
		return nil
	}
	if id == "" {
		return environment.ErrNotActive
	}
	if err := e.doJSON(ctx, http.MethodDelete, "/sandboxes/"+id, nil, nil); err != nil {
		return fmt.Errorf("e2b: destroy: %w", err)
	}

	e.mu.Lock()
	e.state = environment.StateDestroyed
	e.mu.Unlock()
	return nil
}

func (e *E2BSandboxEnvironment) State() environment.SessionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

func (e *E2BSandboxEnvironment) Capabilities() environment.Capabilities {
	return environment.E2BCapabilities
}

func (e *E2BSandboxEnvironment) CreateSnapshot(context.Context, string) (*environment.SnapshotInfo, error) {
	return nil, environment.ErrCapabilityNotSupported
}

func (e *E2BSandboxEnvironment) Rollback(context.Context, string) error {
	return environment.ErrCapabilityNotSupported
}

func (e *E2BSandboxEnvironment) activeSandboxID() (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.state != environment.StateActive || e.sandboxID == "" {
		return "", environment.ErrNotActive
	}
	return e.sandboxID, nil
}

func (e *E2BSandboxEnvironment) executeFileOp(ctx context.Context, sandboxID string, req environment.ToolRequest) (*environment.ToolResponse, error) {
	payload := map[string]any{
		"tool_name": req.ToolName,
		"params":    req.Params,
	}
	var resp struct {
		Content string `json:"content"`
	}
	if err := e.doJSON(ctx, http.MethodPost, "/sandboxes/"+sandboxID+"/filesystem/"+req.ToolName, payload, &resp); err != nil {
		return nil, fmt.Errorf("e2b: execute file op %s: %w", req.ToolName, err)
	}
	return &environment.ToolResponse{
		Content: []ai.ContentBlock{&ai.TextContent{Text: resp.Content}},
	}, nil
}

func (e *E2BSandboxEnvironment) executeCommand(ctx context.Context, sandboxID string, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
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
	if err := e.doJSON(ctx, http.MethodPost, "/sandboxes/"+sandboxID+"/commands/run", payload, &resp); err != nil {
		return nil, fmt.Errorf("e2b: execute command %s: %w", req.ToolName, err)
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

func (e *E2BSandboxEnvironment) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func cloneLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
