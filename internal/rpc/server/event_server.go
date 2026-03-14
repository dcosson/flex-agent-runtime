package server

import (
	"context"
	"sync"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
)

type AgentEventServer struct {
	mu      sync.RWMutex
	streams map[string]map[int]chan *api.AgentEventEnvelope
	nextID  int
}

func NewAgentEventServer() *AgentEventServer {
	return &AgentEventServer{streams: make(map[string]map[int]chan *api.AgentEventEnvelope)}
}

func (s *AgentEventServer) StreamAgentEvents(ctx context.Context, req *api.StreamAgentEventsRequest) (api.AgentEventReceiver, error) {
	if req == nil || req.SessionID == "" {
		return nil, rpc.NewRPCError(rpc.CodeInvalidArgument, "session_id is required", nil)
	}
	ch := make(chan *api.AgentEventEnvelope, 256)
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	if s.streams[req.SessionID] == nil {
		s.streams[req.SessionID] = make(map[int]chan *api.AgentEventEnvelope)
	}
	s.streams[req.SessionID][id] = ch
	s.mu.Unlock()

	closeFn := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if streams := s.streams[req.SessionID]; streams != nil {
			if c, ok := streams[id]; ok {
				close(c)
				delete(streams, id)
			}
			if len(streams) == 0 {
				delete(s.streams, req.SessionID)
			}
		}
		return nil
	}

	go func() {
		<-ctx.Done()
		_ = closeFn()
	}()

	return api.NewAgentEventReceiver(ch, closeFn), nil
}

func (s *AgentEventServer) Publish(sessionID string, evt agent.AgentEvent) {
	envelope := &api.AgentEventEnvelope{SessionID: sessionID, Event: evt}
	s.mu.RLock()
	streams := s.streams[sessionID]
	for _, ch := range streams {
		select {
		case ch <- envelope:
		default:
		}
	}
	s.mu.RUnlock()
}

func (s *AgentEventServer) Sender(sessionID string) api.AgentEventSender {
	return &agentEventSender{server: s, sessionID: sessionID}
}

type agentEventSender struct {
	server    *AgentEventServer
	sessionID string
	closed    bool
	mu        sync.Mutex
}

func (s *agentEventSender) Send(evt *api.AgentEventEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return api.ErrStreamClosed
	}
	if evt == nil {
		return nil
	}
	s.server.Publish(s.sessionID, evt.Event)
	return nil
}

func (s *agentEventSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

var _ api.AgentEventService = (*AgentEventServer)(nil)
