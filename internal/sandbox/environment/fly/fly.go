package fly

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/environment/restapi"
	"golang.org/x/crypto/ssh"
)

const defaultFlyBaseURL = "https://api.machines.dev/v1"

type Option func(*FlySandboxEnvironment)

func WithBaseURL(baseURL string) Option {
	return func(e *FlySandboxEnvironment) {
		if strings.TrimSpace(baseURL) != "" {
			e.api.BaseURL = strings.TrimRight(baseURL, "/")
		}
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(e *FlySandboxEnvironment) {
		if client != nil {
			e.api.HTTPClient = client
		}
	}
}

func WithLogger(logger *slog.Logger) Option {
	return func(e *FlySandboxEnvironment) {
		if logger != nil {
			e.logger = logger
		}
	}
}

func WithSSHUser(user string) Option {
	return func(e *FlySandboxEnvironment) {
		if strings.TrimSpace(user) != "" {
			e.sshUser = user
		}
	}
}

type SSHExecutor func(ctx context.Context, addr, command string, onProgress func(environment.ToolProgress)) (*SSHExecResult, error)

func WithSSHExecutor(exec SSHExecutor) Option {
	return func(e *FlySandboxEnvironment) {
		if exec != nil {
			e.execSSH = exec
		}
	}
}

// FlyOptions carries Fly-specific machine provisioning options.
type FlyOptions struct {
	Image    string
	Region   string
	CPUs     int
	MemoryMB int
	VolumeGB int
	EnvVars  map[string]string
}

// FlySandboxEnvironment implements environment.ExecutionEnvironment using Fly Machines API + SSH.
type FlySandboxEnvironment struct {
	api     restapi.Client
	appName string
	logger  *slog.Logger

	mu        sync.RWMutex
	machineID string
	volumeID  string
	ipAddr    string
	state     environment.SessionState
	created   time.Time
	labels    map[string]string

	sshPrivateKey []byte
	pinnedHostKey string
	sshUser       string
	sshPort       int
	sshTimeout    time.Duration

	execSSH SSHExecutor
}

type SSHExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func NewFlySandboxEnvironment(apiToken, appName string, sshPrivateKey []byte, pinnedHostKey string, opts ...Option) *FlySandboxEnvironment {
	e := &FlySandboxEnvironment{
		api: restapi.Client{
			BaseURL:    defaultFlyBaseURL,
			AuthToken:  strings.TrimSpace(apiToken),
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		},
		appName:       strings.TrimSpace(appName),
		logger:        slog.Default(),
		state:         environment.StateCreating,
		sshPrivateKey: sshPrivateKey,
		pinnedHostKey: strings.TrimSpace(pinnedHostKey),
		sshUser:       "root",
		sshPort:       22,
		sshTimeout:    15 * time.Second,
	}
	e.execSSH = e.defaultSSHExecutor
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *FlySandboxEnvironment) Create(ctx context.Context, config environment.SessionConfig) error {
	if config.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if e.appName == "" {
		return fmt.Errorf("fly: app_name is required")
	}

	var opts FlyOptions
	if config.Options != nil {
		casted, ok := config.Options.(FlyOptions)
		if !ok {
			return fmt.Errorf("fly: Options must be fly.FlyOptions, got %T", config.Options)
		}
		opts = casted
	}
	image := strings.TrimSpace(opts.Image)
	if image == "" {
		image = strings.TrimSpace(config.BaseImage)
	}
	if image == "" {
		return fmt.Errorf("fly: image is required (set BaseImage or FlyOptions.Image)")
	}
	if opts.VolumeGB <= 0 {
		opts.VolumeGB = 5
	}
	if opts.CPUs <= 0 {
		opts.CPUs = 1
	}
	if opts.MemoryMB <= 0 {
		opts.MemoryMB = 512
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	volID, err := e.apiCreateVolume(ctx, opts)
	if err != nil {
		return fmt.Errorf("fly: create volume: %w", err)
	}
	machineID, ip, err := e.apiCreateMachine(ctx, image, opts, volID, config.SessionID)
	if err != nil {
		// Best-effort cleanup to avoid leaking provisioned volumes.
		_ = e.api.DoJSON(ctx, http.MethodDelete, fmt.Sprintf("/apps/%s/volumes/%s", e.appName, volID), nil, nil)
		return fmt.Errorf("fly: create machine: %w", err)
	}

	e.volumeID = volID
	e.machineID = machineID
	e.ipAddr = ip
	e.state = environment.StateActive
	e.created = time.Now()
	e.labels = restapi.CloneLabels(config.Labels)
	return nil
}

func (e *FlySandboxEnvironment) ExecuteTool(ctx context.Context, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	e.mu.RLock()
	state := e.state
	ip := e.ipAddr
	e.mu.RUnlock()

	if state != environment.StateActive || ip == "" {
		return nil, environment.ErrNotActive
	}

	var command string
	var err error
	if environment.IsFileOp(req.ToolName) {
		if req.ToolName == "edit_file" {
			return e.executeEditFile(ctx, addrForIP(ip, e.sshPort), req, onProgress)
		}
		command, err = buildFileCommand(req)
	} else {
		command, err = buildProcessCommand(req)
	}
	if err != nil {
		return nil, err
	}

	addr := addrForIP(ip, e.sshPort)
	result, err := e.execSSH(ctx, addr, command, onProgress)
	if err != nil {
		return nil, fmt.Errorf("fly: execute %s: %w", req.ToolName, err)
	}

	text := result.Stdout
	if result.Stderr != "" {
		if text != "" {
			text += "\n"
		}
		text += result.Stderr
	}
	exitCode := result.ExitCode
	return &environment.ToolResponse{
		Content:  []ai.ContentBlock{&ai.TextContent{Text: text}},
		ExitCode: &exitCode,
	}, nil
}

func (e *FlySandboxEnvironment) executeEditFile(ctx context.Context, addr string, req environment.ToolRequest, onProgress func(environment.ToolProgress)) (*environment.ToolResponse, error) {
	path, _ := req.Params["path"].(string)
	oldStr, _ := req.Params["old_string"].(string)
	newStr, _ := req.Params["new_string"].(string)
	replaceAll, _ := req.Params["replace_all"].(bool)

	if path == "" {
		return &environment.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: "missing required parameter: path"}}}, nil
	}
	if oldStr == "" {
		return &environment.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: "missing required parameter: old_string"}}}, nil
	}

	readCmd, err := buildFileCommand(environment.ToolRequest{
		ToolName: "read_file",
		Params:   map[string]any{"path": path},
	})
	if err != nil {
		return nil, err
	}
	readRes, err := e.execSSH(ctx, addr, readCmd, onProgress)
	if err != nil {
		return nil, fmt.Errorf("fly: edit_file read: %w", err)
	}
	if readRes.ExitCode != 0 {
		msg := strings.TrimSpace(readRes.Stderr)
		if msg == "" {
			msg = "file read failed"
		}
		return &environment.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: msg}}}, nil
	}

	updated, msg, changed := applyEdit(readRes.Stdout, oldStr, newStr, replaceAll, path)
	if !changed {
		return &environment.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: msg}}}, nil
	}

	writeCmd, err := buildFileCommand(environment.ToolRequest{
		ToolName: "write_file",
		Params: map[string]any{
			"path":    path,
			"content": updated,
		},
	})
	if err != nil {
		return nil, err
	}
	writeRes, err := e.execSSH(ctx, addr, writeCmd, onProgress)
	if err != nil {
		return nil, fmt.Errorf("fly: edit_file write: %w", err)
	}
	if writeRes.ExitCode != 0 {
		msg := strings.TrimSpace(writeRes.Stderr)
		if msg == "" {
			msg = "file write failed"
		}
		return &environment.ToolResponse{Content: []ai.ContentBlock{&ai.TextContent{Text: msg}}}, nil
	}

	return &environment.ToolResponse{
		Content: []ai.ContentBlock{&ai.TextContent{Text: msg}},
	}, nil
}

func (e *FlySandboxEnvironment) Pause(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state != environment.StateActive || e.machineID == "" {
		return environment.ErrNotActive
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, fmt.Sprintf("/apps/%s/machines/%s/suspend", e.appName, e.machineID), map[string]any{}, nil); err != nil {
		return fmt.Errorf("fly: suspend: %w", err)
	}
	e.state = environment.StatePaused
	return nil
}

func (e *FlySandboxEnvironment) Resume(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state != environment.StatePaused || e.machineID == "" {
		return environment.ErrNotActive
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, fmt.Sprintf("/apps/%s/machines/%s/start", e.appName, e.machineID), map[string]any{}, nil); err != nil {
		return fmt.Errorf("fly: start: %w", err)
	}
	e.state = environment.StateActive
	return nil
}

func (e *FlySandboxEnvironment) Destroy(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.state == environment.StateDestroyed {
		return nil
	}
	if e.machineID == "" {
		return environment.ErrNotActive
	}

	if err := e.api.DoJSON(ctx, http.MethodDelete, fmt.Sprintf("/apps/%s/machines/%s", e.appName, e.machineID), nil, nil); err != nil {
		return fmt.Errorf("fly: destroy machine: %w", err)
	}
	if e.volumeID != "" {
		if err := e.api.DoJSON(ctx, http.MethodDelete, fmt.Sprintf("/apps/%s/volumes/%s", e.appName, e.volumeID), nil, nil); err != nil {
			return fmt.Errorf("fly: destroy volume: %w", err)
		}
	}

	e.state = environment.StateDestroyed
	return nil
}

func (e *FlySandboxEnvironment) State() environment.SessionState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

func (e *FlySandboxEnvironment) Capabilities() environment.Capabilities {
	return environment.FlyCapabilities
}

func (e *FlySandboxEnvironment) CreateSnapshot(context.Context, string) (*environment.SnapshotInfo, error) {
	return nil, environment.ErrCapabilityNotSupported
}

func (e *FlySandboxEnvironment) Rollback(context.Context, string) error {
	return environment.ErrCapabilityNotSupported
}

func (e *FlySandboxEnvironment) apiCreateVolume(ctx context.Context, opts FlyOptions) (string, error) {
	payload := map[string]any{
		"size_gb": opts.VolumeGB,
		"region":  opts.Region,
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, fmt.Sprintf("/apps/%s/volumes", e.appName), payload, &resp); err != nil {
		return "", err
	}
	if resp.ID == "" {
		return "", fmt.Errorf("missing volume id")
	}
	return resp.ID, nil
}

func (e *FlySandboxEnvironment) apiCreateMachine(ctx context.Context, image string, opts FlyOptions, volID, sessionID string) (machineID, ip string, err error) {
	payload := map[string]any{
		"name": "sess-" + sessionID,
		"config": map[string]any{
			"image": image,
			"env":   opts.EnvVars,
		},
		"guest": map[string]any{
			"cpus":   opts.CPUs,
			"memory": opts.MemoryMB,
		},
		"mounts": []map[string]any{
			{"volume": volID, "path": "/workspace"},
		},
	}
	var resp struct {
		ID        string `json:"id"`
		PrivateIP string `json:"private_ip"`
	}
	if err := e.api.DoJSON(ctx, http.MethodPost, fmt.Sprintf("/apps/%s/machines", e.appName), payload, &resp); err != nil {
		return "", "", err
	}
	if resp.ID == "" || resp.PrivateIP == "" {
		return "", "", fmt.Errorf("missing machine id/private ip")
	}
	return resp.ID, resp.PrivateIP, nil
}

func (e *FlySandboxEnvironment) defaultSSHExecutor(ctx context.Context, addr, command string, onProgress func(environment.ToolProgress)) (*SSHExecResult, error) {
	cfg, err := e.sshClientConfig()
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: e.sshTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	runErr := session.Run(command)

	result := &SSHExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}
	if onProgress != nil {
		if result.Stdout != "" {
			onProgress(environment.ToolProgress{Content: result.Stdout})
		}
		if result.Stderr != "" {
			onProgress(environment.ToolProgress{Content: result.Stderr, IsError: true})
		}
	}

	if runErr != nil {
		var ee *ssh.ExitError
		if errorsAs(runErr, &ee) {
			result.ExitCode = ee.ExitStatus()
			return result, nil
		}
		return nil, runErr
	}
	return result, nil
}

func (e *FlySandboxEnvironment) sshClientConfig() (*ssh.ClientConfig, error) {
	if len(e.sshPrivateKey) == 0 {
		return nil, fmt.Errorf("fly: ssh private key is required")
	}
	if e.pinnedHostKey == "" {
		return nil, fmt.Errorf("fly: pinned host key is required")
	}
	signer, err := ssh.ParsePrivateKey(e.sshPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("fly: parse ssh private key: %w", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(e.pinnedHostKey))
	if err != nil {
		return nil, fmt.Errorf("fly: parse pinned host key: %w", err)
	}
	return &ssh.ClientConfig{
		User:            e.sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.FixedHostKey(pub),
		Timeout:         e.sshTimeout,
	}, nil
}

func buildFileCommand(req environment.ToolRequest) (string, error) {
	params := req.Params
	switch req.ToolName {
	case "read_file":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("fly: read_file requires path")
		}
		return "cat " + shQuote(path), nil
	case "write_file":
		path, _ := params["path"].(string)
		content, _ := params["content"].(string)
		if path == "" {
			return "", fmt.Errorf("fly: write_file requires path")
		}
		dir := filepath.Dir(path)
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		return fmt.Sprintf("mkdir -p %s && printf %%s %s | base64 -d > %s",
			shQuote(dir), shQuote(encoded), shQuote(path)), nil
	case "grep":
		pattern, _ := params["pattern"].(string)
		path, _ := params["path"].(string)
		if pattern == "" {
			return "", fmt.Errorf("fly: grep requires pattern")
		}
		if path == "" {
			path = "."
		}
		return fmt.Sprintf("grep -R -n -- %s %s", shQuote(pattern), shQuote(path)), nil
	case "glob":
		pattern, _ := params["pattern"].(string)
		if pattern == "" {
			return "", fmt.Errorf("fly: glob requires pattern")
		}
		return fmt.Sprintf("find . -path %s -print", shQuote(pattern)), nil
	case "edit_file":
		path, _ := params["path"].(string)
		if path == "" {
			return "", fmt.Errorf("fly: edit_file requires path")
		}
		return "cat " + shQuote(path), nil
	default:
		return "", fmt.Errorf("fly: unsupported file op: %s", req.ToolName)
	}
}

func applyEdit(content, oldStr, newStr string, replaceAll bool, path string) (string, string, bool) {
	count := strings.Count(content, oldStr)
	if count == 0 {
		return content, fmt.Sprintf("old_string not found in %s", path), false
	}
	if !replaceAll && count > 1 {
		return content, fmt.Sprintf("old_string found %d times in %s — provide more context to uniquely identify the target, or set replace_all=true", count, path), false
	}
	if replaceAll {
		return strings.ReplaceAll(content, oldStr, newStr), fmt.Sprintf("edited %s: %d replacement(s) made", path, count), true
	}
	return strings.Replace(content, oldStr, newStr, 1), fmt.Sprintf("edited %s: 1 replacement(s) made", path), true
}

func buildProcessCommand(req environment.ToolRequest) (string, error) {
	switch req.ToolName {
	case "bash":
		cmd, _ := req.Params["command"].(string)
		if cmd == "" {
			return "", fmt.Errorf("fly: bash requires command")
		}
		return cmd, nil
	case "git_status":
		return "git status --porcelain=v1", nil
	case "git_diff":
		return "git diff", nil
	case "git_log":
		return "git log --oneline -n 20", nil
	case "git_show":
		return "git show", nil
	default:
		if cmd, _ := req.Params["command"].(string); cmd != "" {
			return cmd, nil
		}
		return "", fmt.Errorf("fly: unsupported process op: %s", req.ToolName)
	}
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func addrForIP(ip string, port int) string {
	return net.JoinHostPort(ip, fmt.Sprintf("%d", port))
}

// errorsAs is a tiny indirection so tests can replace behavior if needed.
var errorsAs = func(err error, target any) bool {
	return errors.As(err, target)
}
