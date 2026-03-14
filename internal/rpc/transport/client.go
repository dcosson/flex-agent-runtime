package transport

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/termmux"
)

type ClientConfig struct {
	APIVersion      string
	MaxMessageBytes int
	AuthHook        AuthHook
}

type SandboxClient struct {
	CreateSession  *connect.Client[api.CreateSessionRequest, api.CreateSessionResponse]
	GetSession     *connect.Client[api.GetSessionRequest, api.GetSessionResponse]
	PauseSession   *connect.Client[api.PauseSessionRequest, api.PauseSessionResponse]
	ResumeSession  *connect.Client[api.ResumeSessionRequest, api.ResumeSessionResponse]
	DestroySession *connect.Client[api.DestroySessionRequest, api.DestroySessionResponse]
	ExecuteTool    *connect.Client[api.ExecuteToolRequest, api.ExecuteToolResponse]
	ExecuteStream  *connect.Client[api.ExecuteToolRequest, api.ExecuteToolStreamMessage]
	TurnComplete   *connect.Client[api.TurnCompleteRequest, api.TurnCompleteResponse]
	CreateSnapshot *connect.Client[api.CreateSnapshotRequest, api.CreateSnapshotResponse]
	Rollback       *connect.Client[api.RollbackSessionRequest, api.RollbackSessionResponse]
	ListSnapshots  *connect.Client[api.ListSnapshotsRequest, api.ListSnapshotsResponse]
	HealthCheck    *connect.Client[api.HealthCheckRequest, api.HealthCheckResponse]
}

type EventClient struct {
	Stream *connect.Client[api.StreamAgentEventsRequest, api.AgentEventEnvelope]
}

type TerminalClient struct {
	Stream *connect.Client[termmux.TerminalClientMessage, termmux.TerminalServerMessage]
}

func NewSandboxClient(httpClient connect.HTTPClient, baseURL string, cfg ClientConfig) *SandboxClient {
	return &SandboxClient{
		CreateSession:  connect.NewClient[api.CreateSessionRequest, api.CreateSessionResponse](httpClient, baseURL+ProcedureSandboxCreateSession, clientOptions(cfg)...),
		GetSession:     connect.NewClient[api.GetSessionRequest, api.GetSessionResponse](httpClient, baseURL+ProcedureSandboxGetSession, clientOptions(cfg)...),
		PauseSession:   connect.NewClient[api.PauseSessionRequest, api.PauseSessionResponse](httpClient, baseURL+ProcedureSandboxPauseSession, clientOptions(cfg)...),
		ResumeSession:  connect.NewClient[api.ResumeSessionRequest, api.ResumeSessionResponse](httpClient, baseURL+ProcedureSandboxResumeSession, clientOptions(cfg)...),
		DestroySession: connect.NewClient[api.DestroySessionRequest, api.DestroySessionResponse](httpClient, baseURL+ProcedureSandboxDestroySession, clientOptions(cfg)...),
		ExecuteTool:    connect.NewClient[api.ExecuteToolRequest, api.ExecuteToolResponse](httpClient, baseURL+ProcedureSandboxExecuteTool, clientOptions(cfg)...),
		ExecuteStream:  connect.NewClient[api.ExecuteToolRequest, api.ExecuteToolStreamMessage](httpClient, baseURL+ProcedureSandboxExecuteStream, clientOptions(cfg)...),
		TurnComplete:   connect.NewClient[api.TurnCompleteRequest, api.TurnCompleteResponse](httpClient, baseURL+ProcedureSandboxTurnComplete, clientOptions(cfg)...),
		CreateSnapshot: connect.NewClient[api.CreateSnapshotRequest, api.CreateSnapshotResponse](httpClient, baseURL+ProcedureSandboxCreateSnapshot, clientOptions(cfg)...),
		Rollback:       connect.NewClient[api.RollbackSessionRequest, api.RollbackSessionResponse](httpClient, baseURL+ProcedureSandboxRollback, clientOptions(cfg)...),
		ListSnapshots:  connect.NewClient[api.ListSnapshotsRequest, api.ListSnapshotsResponse](httpClient, baseURL+ProcedureSandboxListSnapshots, clientOptions(cfg)...),
		HealthCheck:    connect.NewClient[api.HealthCheckRequest, api.HealthCheckResponse](httpClient, baseURL+ProcedureSandboxHealthCheck, clientOptions(cfg)...),
	}
}

func NewEventClient(httpClient connect.HTTPClient, baseURL string, cfg ClientConfig) *EventClient {
	return &EventClient{
		Stream: connect.NewClient[api.StreamAgentEventsRequest, api.AgentEventEnvelope](httpClient, baseURL+ProcedureEventsStream, clientOptions(cfg)...),
	}
}

func NewTerminalClient(httpClient connect.HTTPClient, baseURL string, cfg ClientConfig) *TerminalClient {
	return &TerminalClient{
		Stream: connect.NewClient[termmux.TerminalClientMessage, termmux.TerminalServerMessage](httpClient, baseURL+ProcedureTerminalStream, clientOptions(cfg)...),
	}
}

func clientOptions(cfg ClientConfig) []connect.ClientOption {
	if cfg.APIVersion == "" {
		cfg.APIVersion = "v1"
	}
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = 16 << 20
	}
	return []connect.ClientOption{
		connect.WithCodec(JSONCodec{}),
		connect.WithReadMaxBytes(cfg.MaxMessageBytes),
		connect.WithSendMaxBytes(cfg.MaxMessageBytes),
		connect.WithInterceptors(clientPolicyInterceptor{cfg: cfg}),
	}
}

func HeaderTokenAuth(name, token string) AuthHook {
	if name == "" {
		name = "authorization"
	}
	return func(_ context.Context, _ string, headers http.Header) error {
		headers.Set(name, token)
		return nil
	}
}

type clientPolicyInterceptor struct {
	cfg ClientConfig
}

func (i clientPolicyInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.apply(ctx, req.Spec().Procedure, req.Header()); err != nil {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		return next(ctx, req)
	}
}

func (i clientPolicyInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		_ = i.apply(ctx, spec.Procedure, conn.RequestHeader())
		return conn
	}
}

func (i clientPolicyInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

func (i clientPolicyInterceptor) apply(ctx context.Context, procedure string, headers http.Header) error {
	headers.Set(rpc.APIVersionHeaderName(), i.cfg.APIVersion)
	if i.cfg.AuthHook != nil {
		if err := i.cfg.AuthHook(ctx, procedure, headers); err != nil {
			return err
		}
	}
	return nil
}
