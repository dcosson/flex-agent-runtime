package harness

import (
	"context"

	"github.com/anthropics/flex-agent-runtime/internal/rpc/api"
)

type SessionLifecycle struct {
	Service api.SandboxService
}

func NewSessionLifecycle(service api.SandboxService) *SessionLifecycle {
	return &SessionLifecycle{Service: service}
}

func (l *SessionLifecycle) Create(ctx context.Context, baseSnapshot, sessionID string) (*api.Session, error) {
	resp, err := l.Service.CreateSession(ctx, &api.CreateSessionRequest{
		BaseSnapshot: baseSnapshot,
		SessionID:    sessionID,
	})
	if err != nil {
		return nil, err
	}
	return resp.Session, nil
}

func (l *SessionLifecycle) Get(ctx context.Context, sessionID string) (*api.Session, error) {
	resp, err := l.Service.GetSession(ctx, &api.GetSessionRequest{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	return resp.Session, nil
}

func (l *SessionLifecycle) Pause(ctx context.Context, sessionID string) error {
	_, err := l.Service.PauseSession(ctx, &api.PauseSessionRequest{SessionID: sessionID})
	return err
}

func (l *SessionLifecycle) Resume(ctx context.Context, sessionID string) error {
	_, err := l.Service.ResumeSession(ctx, &api.ResumeSessionRequest{SessionID: sessionID})
	return err
}

func (l *SessionLifecycle) Destroy(ctx context.Context, sessionID string) error {
	_, err := l.Service.DestroySession(ctx, &api.DestroySessionRequest{SessionID: sessionID})
	return err
}
