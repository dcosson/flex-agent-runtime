package api

import (
	"context"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/agent"
)

type CreateSessionRequest struct {
	BaseSnapshot string
	SessionID    string
	Quota        int64
	Labels       map[string]string
}

type CreateSessionResponse struct {
	Session            *Session
	ServerCapabilities Capabilities
}

type Capabilities struct {
	Snapshots         bool
	Rollback          bool
	Pause             bool
	StreamingProgress bool
	TierRouting       bool
}

type GetSessionRequest struct {
	SessionID string
}

type GetSessionResponse struct {
	Session *Session
}

type PauseSessionRequest struct {
	SessionID string
}

type PauseSessionResponse struct{}

type ResumeSessionRequest struct {
	SessionID string
}

type ResumeSessionResponse struct{}

type DestroySessionRequest struct {
	SessionID string
}

type DestroySessionResponse struct{}

type TurnCompleteRequest struct {
	SessionID string
}

type TurnCompleteResponse struct {
	SnapshotID string
	TurnNumber int
	SpaceUsed  int64
}

type CreateSnapshotRequest struct {
	SessionID string
	Name      string
}

type CreateSnapshotResponse struct {
	SnapshotID string
	TurnNumber int
	SpaceUsed  int64
}

type RollbackSessionRequest struct {
	SessionID  string
	SnapshotID string
}

type RollbackSessionResponse struct{}

type ListSnapshotsRequest struct {
	SessionID string
}

type ListSnapshotsResponse struct {
	Snapshots []Snapshot
}

type LaunchProcessRequest struct {
	SessionID  string
	Binary     string
	Args       []string
	Env        map[string]string
	ExposePort int
}

type LaunchProcessResponse struct {
	ProcessID string
	Address   string
	Status    ProcessStatus
}

type KillProcessRequest struct {
	SessionID string
	ProcessID string
	Signal    int
}

type KillProcessResponse struct{}

type GetProcessStatusRequest struct {
	SessionID string
	ProcessID string
}

type GetProcessStatusResponse struct {
	Status   ProcessStatus
	ExitCode *int
}

type ProcessStatus string

const (
	ProcessStatusStarting ProcessStatus = "starting"
	ProcessStatusRunning  ProcessStatus = "running"
	ProcessStatusExited   ProcessStatus = "exited"
)

type ExecuteToolRequest struct {
	SessionID  string
	ToolCallID string
	ToolName   string
	Params     map[string]any
	Resources  *ResourceSpec
}

type ExecuteToolResponse struct {
	SessionID     string
	ToolCallID    string
	ToolName      string
	Content       string
	ContentBlocks []ContentBlock
	SnapshotID    string
	ExitCode      *int
	Tier          int
	Duration      time.Duration
}

type ToolProgress struct {
	Content string
	IsError bool
}

type ExecuteToolStreamMessage struct {
	Progress *ToolProgress
	Response *ExecuteToolResponse
}

type ResourceSpec struct {
	CPUs  float64
	MemMB int
}

type ContentBlock struct {
	Type              string
	Text              string
	TextSignature     string
	Thinking          string
	ThinkingSignature string
	Redacted          bool
	ImageData         string
	ImageMimeType     string
	ToolCallID        string
	ToolCallName      string
	ToolCallArguments map[string]any
	ThoughtSignature  string
}

type Session struct {
	ID         string
	State      string
	Mountpoint string
	TurnCount  int
	SnapCount  int
	Created    time.Time
	Labels     map[string]string
	SpaceUsed  int64
}

type Snapshot struct {
	Name      string
	Dataset   string
	Used      int64
	Refer     int64
	CreatedAt time.Time
	Holds     int
}

type HealthCheckRequest struct{}

type HealthCheckResponse struct {
	Status       string
	PoolState    string
	SessionCount int
	ActiveTools  int
	Uptime       time.Duration
	Errors       []string
}

// Event streaming contracts.
type StreamAgentEventsRequest struct {
	SessionID string
}

type AgentEventEnvelope struct {
	SessionID string
	Event     agent.AgentEvent
}

type AgentEventSender interface {
	Send(*AgentEventEnvelope) error
	Close() error
}

type AgentEventReceiver interface {
	Recv() (*AgentEventEnvelope, error)
	Close() error
}

type ExecuteToolStreamReceiver interface {
	Recv() (*ExecuteToolStreamMessage, error)
	Close() error
}

// Service interfaces used by client/server adapters.
type SandboxService interface {
	CreateSession(ctx context.Context, req *CreateSessionRequest) (*CreateSessionResponse, error)
	GetSession(ctx context.Context, req *GetSessionRequest) (*GetSessionResponse, error)
	PauseSession(ctx context.Context, req *PauseSessionRequest) (*PauseSessionResponse, error)
	ResumeSession(ctx context.Context, req *ResumeSessionRequest) (*ResumeSessionResponse, error)
	DestroySession(ctx context.Context, req *DestroySessionRequest) (*DestroySessionResponse, error)
	LaunchProcess(ctx context.Context, req *LaunchProcessRequest) (*LaunchProcessResponse, error)
	KillProcess(ctx context.Context, req *KillProcessRequest) (*KillProcessResponse, error)
	GetProcessStatus(ctx context.Context, req *GetProcessStatusRequest) (*GetProcessStatusResponse, error)
	ExecuteTool(ctx context.Context, req *ExecuteToolRequest) (*ExecuteToolResponse, error)
	ExecuteToolStream(ctx context.Context, req *ExecuteToolRequest) (ExecuteToolStreamReceiver, error)
	TurnComplete(ctx context.Context, req *TurnCompleteRequest) (*TurnCompleteResponse, error)
	CreateSnapshot(ctx context.Context, req *CreateSnapshotRequest) (*CreateSnapshotResponse, error)
	RollbackSession(ctx context.Context, req *RollbackSessionRequest) (*RollbackSessionResponse, error)
	ListSnapshots(ctx context.Context, req *ListSnapshotsRequest) (*ListSnapshotsResponse, error)
	HealthCheck(ctx context.Context, req *HealthCheckRequest) (*HealthCheckResponse, error)
}

type AgentEventService interface {
	StreamAgentEvents(ctx context.Context, req *StreamAgentEventsRequest) (AgentEventReceiver, error)
}
