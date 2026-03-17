package api

import (
	"context"
)

// AgentService manages agent loop sessions.
// Implemented by an in-process service and an RPC-backed client.
type AgentService interface {
	CreateSession(ctx context.Context, req *CreateAgentSessionRequest) (*CreateAgentSessionResponse, error)
	GetSession(ctx context.Context, req *GetAgentSessionRequest) (*GetAgentSessionResponse, error)
	ListSessions(ctx context.Context, req *ListAgentSessionsRequest) (*ListAgentSessionsResponse, error)
	SendMessage(ctx context.Context, req *SendMessageRequest) (EventReceiver, error)
	Continue(ctx context.Context, req *ContinueRequest) (EventReceiver, error)
	Steer(ctx context.Context, req *SteerRequest) (*SteerResponse, error)
	FollowUp(ctx context.Context, req *FollowUpRequest) (*FollowUpResponse, error)
	Abort(ctx context.Context, req *AbortRequest) (*AbortResponse, error)
	SubscribeEvents(ctx context.Context, req *SubscribeEventsRequest) (EventReceiver, error)
	ResumeSession(ctx context.Context, req *ResumeSessionRequest) (*ResumeSessionResponse, error)
	DestroySession(ctx context.Context, req *DestroyAgentSessionRequest) (*DestroyAgentSessionResponse, error)
	Close() error
}

// EventReceiver is the transport-neutral stream interface for agent events.
type EventReceiver interface {
	Recv() (*AgentEvent, error)
	Close() error
}
