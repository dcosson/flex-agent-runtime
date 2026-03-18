package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	agentapi "github.com/anthropics/flex-agent-runtime/internal/agent/api"
	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

func TestServiceSessionLifecycle(t *testing.T) {
	stack := newServiceTestStack(t)

	created, err := stack.createSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if created.SessionID != "sess-1" {
		t.Fatalf("session_id = %q, want sess-1", created.SessionID)
	}
	if created.State != string(StateIdle) {
		t.Fatalf("state = %q, want %q", created.State, StateIdle)
	}

	get, err := stack.svc.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if get.SessionID != "sess-1" {
		t.Fatalf("get session_id = %q", get.SessionID)
	}
	if get.ConversationLen != 0 {
		t.Fatalf("conversation_len = %d, want 0", get.ConversationLen)
	}

	list, err := stack.svc.ListSessions(context.Background(), &agentapi.ListAgentSessionsRequest{})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(list.Sessions) != 1 || list.Sessions[0].SessionID != "sess-1" {
		t.Fatalf("unexpected list response: %+v", list.Sessions)
	}

	if _, err := stack.svc.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: "sess-1"}); err != nil {
		t.Fatalf("destroy session: %v", err)
	}
	if _, err := stack.svc.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "sess-1"}); err == nil {
		t.Fatal("expected not found after destroy")
	}
}

func TestCreateSessionGeneratesIDIfEmpty(t *testing.T) {
	stack := newServiceTestStack(t)
	resp, err := stack.createSession(context.Background(), "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if resp.SessionID == "" {
		t.Fatal("expected generated session id")
	}
}

func TestCreateSessionDuplicateID(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "dup"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := stack.createSession(context.Background(), "dup"); err == nil {
		t.Fatal("expected duplicate create to fail")
	} else {
		assertRPCCode(t, err, CodeAlreadyExists)
	}
}

func TestCreateSessionExceedsMaxSessions(t *testing.T) {
	stack := newServiceTestStack(t, WithMaxSessions(2))
	if _, err := stack.createSession(context.Background(), "s1"); err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if _, err := stack.createSession(context.Background(), "s2"); err != nil {
		t.Fatalf("create s2: %v", err)
	}
	if _, err := stack.createSession(context.Background(), "s3"); err == nil {
		t.Fatal("expected max sessions failure")
	} else {
		assertRPCCode(t, err, CodeResourceExhausted)
	}
}

func TestCreateSessionInvalidModel(t *testing.T) {
	stack := newServiceTestStack(t)
	_, err := stack.svc.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			SessionID: "bad-model",
			Driver:    "mock",
			Model:     "missing-model",
			Provider:  "missing-provider",
		},
	})
	if err == nil {
		t.Fatal("expected invalid model error")
	}
	assertRPCCode(t, err, CodeInvalidArgument)
}

func TestConcurrentSessionsIndependent(t *testing.T) {
	stack := newServiceTestStack(t)
	ctx := context.Background()
	const sessions = 5

	var wg sync.WaitGroup
	errCh := make(chan error, sessions)

	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessionID := fmt.Sprintf("conc-%d", i)
			if _, err := stack.createSession(ctx, sessionID); err != nil {
				errCh <- fmt.Errorf("create %s: %w", sessionID, err)
				return
			}
			recv, err := stack.svc.SendMessage(ctx, &agentapi.SendMessageRequest{
				SessionID: sessionID,
				Message:   "hello",
			})
			if err != nil {
				errCh <- fmt.Errorf("send %s: %w", sessionID, err)
				return
			}
			evts := collectAllEvents(t, recv)
			if len(evts) == 0 {
				errCh <- fmt.Errorf("%s: no events", sessionID)
				return
			}
			for _, evt := range evts {
				if evt.SessionID != sessionID {
					errCh <- fmt.Errorf("%s: wrong session id on event %q", sessionID, evt.SessionID)
					return
				}
			}
			if _, err := stack.svc.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: sessionID}); err != nil {
				errCh <- fmt.Errorf("destroy %s: %w", sessionID, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestSendMessageBusyError(t *testing.T) {
	stack := newServiceTestStack(t)
	blockCh := make(chan struct{})
	driver := &mockAgentDriver{BlockOnStart: blockCh}
	stack.factory.SetDriver("busy", driver)

	if _, err := stack.createSession(context.Background(), "busy"); err != nil {
		t.Fatalf("create: %v", err)
	}
	firstRecv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "busy",
		Message:   "first",
	})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	defer firstRecv.Close()

	if !waitForCondition(500*time.Millisecond, func() bool { return driver.StartCount() > 0 }) {
		t.Fatal("first turn did not start")
	}

	_, err = stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "busy",
		Message:   "second",
	})
	if err == nil {
		t.Fatal("expected busy error")
	}
	assertRPCCode(t, err, CodeFailedPrecondition)

	close(blockCh)
}

func TestDestroySessionDuringActiveTurn(t *testing.T) {
	stack := newServiceTestStack(t)
	blockCh := make(chan struct{})
	driver := &mockAgentDriver{BlockOnStart: blockCh}
	stack.factory.SetDriver("destroy-active", driver)

	if _, err := stack.createSession(context.Background(), "destroy-active"); err != nil {
		t.Fatalf("create: %v", err)
	}

	recv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "destroy-active",
		Message:   "working",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	if _, err := stack.svc.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: "destroy-active"}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := stack.svc.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "destroy-active"}); err == nil {
		t.Fatal("expected session not found")
	}
	assertRPCCode(t, recv.Close(), "")
	close(blockCh)
}

func TestSteerWhileIdleFailedPrecondition(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "steer-idle"); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := stack.svc.Steer(context.Background(), &agentapi.SteerRequest{
		SessionID: "steer-idle",
		Message:   "go",
	})
	if err == nil {
		t.Fatal("expected steer failure while idle")
	}
	assertRPCCode(t, err, CodeFailedPrecondition)
}

func TestFollowUpValidInAnyState(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "follow-idle"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := stack.svc.FollowUp(context.Background(), &agentapi.FollowUpRequest{
		SessionID: "follow-idle",
		Message:   "then do x",
	}); err != nil {
		t.Fatalf("followup on idle: %v", err)
	}
}

func TestCloseRejectsNewSessions(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "pre-close"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := stack.svc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := stack.createSession(context.Background(), "post-close")
	if err == nil {
		t.Fatal("expected create to fail after close")
	}
	assertRPCCode(t, err, CodeUnavailable)
}

func TestCloseDrainsActiveSessions(t *testing.T) {
	stack := newServiceTestStack(t, WithCloseDrainTimeout(2*time.Second))
	blockCh := make(chan struct{})
	driver := &mockAgentDriver{BlockOnStart: blockCh}
	stack.factory.SetDriver("drain", driver)

	if _, err := stack.createSession(context.Background(), "drain"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "drain",
		Message:   "hello",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- stack.svc.Close()
	}()

	close(blockCh)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not complete in time")
	}
}

func TestResumeSessionValidation(t *testing.T) {
	stack := newServiceTestStack(t)

	mkUser := func(turn int, text string) agentapi.AgentMessageRecord {
		rec, err := agentapi.AgentMessageToRecord(agentapi.AgentMessage{
			Turn:      turn,
			CreatedAt: time.Unix(int64(turn), 0).UTC(),
			Message: &ai.UserMessage{
				Content:   []ai.ContentBlock{&ai.TextContent{Text: text}},
				Timestamp: int64(turn) * 1000,
			},
		})
		if err != nil {
			t.Fatalf("mkUser record: %v", err)
		}
		return rec
	}
	mkToolResult := func(turn int, toolName string) agentapi.AgentMessageRecord {
		rec, err := agentapi.AgentMessageToRecord(agentapi.AgentMessage{
			Turn:      turn,
			CreatedAt: time.Unix(int64(turn), 0).UTC(),
			Message: &ai.ToolResultMessage{
				ToolCallID: "call-1",
				ToolName:   toolName,
				Content:    []ai.ContentBlock{&ai.TextContent{Text: "done"}},
				Timestamp:  int64(turn) * 1000,
			},
		})
		if err != nil {
			t.Fatalf("mkToolResult record: %v", err)
		}
		return rec
	}

	t.Run("schema mismatch", func(t *testing.T) {
		_, err := stack.svc.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
			SessionConfig: stack.baseConfig("resume-schema"),
			SchemaVersion: 999,
		})
		if err == nil {
			t.Fatal("expected schema mismatch")
		}
		assertRPCCode(t, err, CodeInvalidArgument)
	})

	t.Run("malformed record rejected", func(t *testing.T) {
		_, err := stack.svc.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
			SessionConfig: stack.baseConfig("resume-bad-record"),
			SchemaVersion: agentapi.CurrentConversationSchemaVersion,
			ConversationLog: []agentapi.AgentMessageRecord{
				{Turn: 1, Role: agentapi.AgentMessageRoleAssistant, Content: json.RawMessage(`{"content_blocks":[`)},
			},
		})
		if err == nil {
			t.Fatal("expected parse error")
		}
		assertRPCCode(t, err, CodeInvalidArgument)
	})

	t.Run("unknown role rejected", func(t *testing.T) {
		_, err := stack.svc.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
			SessionConfig: stack.baseConfig("resume-bad-role"),
			SchemaVersion: agentapi.CurrentConversationSchemaVersion,
			ConversationLog: []agentapi.AgentMessageRecord{
				{Turn: 1, Role: "bad_role", Content: json.RawMessage(`{}`)},
			},
		})
		if err == nil {
			t.Fatal("expected unknown role error")
		}
		assertRPCCode(t, err, CodeInvalidArgument)
	})

	t.Run("turn decrease rejected", func(t *testing.T) {
		_, err := stack.svc.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
			SessionConfig: stack.baseConfig("resume-turn-decrease"),
			SchemaVersion: agentapi.CurrentConversationSchemaVersion,
			ConversationLog: []agentapi.AgentMessageRecord{
				mkUser(2, "a"),
				mkUser(1, "b"),
			},
		})
		if err == nil {
			t.Fatal("expected turn decrease error")
		}
		assertRPCCode(t, err, CodeInvalidArgument)
	})

	t.Run("warnings allow resume", func(t *testing.T) {
		resp, err := stack.svc.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
			SessionConfig: stack.baseConfig("resume-warn"),
			SchemaVersion: agentapi.CurrentConversationSchemaVersion,
			ConversationLog: []agentapi.AgentMessageRecord{
				mkUser(1, "first"),
				mkUser(3, "second"),             // non-contiguous + same-role warning
				mkToolResult(3, "unknown_tool"), // tool compatibility warning
			},
		})
		if err != nil {
			t.Fatalf("resume with warnings: %v", err)
		}
		if resp.ConversationLen != 3 {
			t.Fatalf("conversation_len = %d, want 3", resp.ConversationLen)
		}
		if len(resp.Warnings) < 3 {
			t.Fatalf("expected warnings, got %+v", resp.Warnings)
		}
	})
}

func TestEventPublisherReceivesAllEvents(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "pub"); err != nil {
		t.Fatalf("create: %v", err)
	}
	recv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "pub",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = collectAllEvents(t, recv)

	evts := stack.publisher.EventsForSession("pub")
	if len(evts) == 0 {
		t.Fatal("expected publisher events")
	}
	if !containsEventType(evts, EventTurnStarted) || !containsEventType(evts, EventTurnCompleted) {
		t.Fatalf("missing required event types: %+v", eventTypes(evts))
	}
}

func TestEventPublisherStopsAfterDestroy(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "pub-destroy"); err != nil {
		t.Fatalf("create: %v", err)
	}
	recv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "pub-destroy",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = collectAllEvents(t, recv)
	countBefore := len(stack.publisher.EventsForSession("pub-destroy"))

	if _, err := stack.svc.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: "pub-destroy"}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	countAfter := len(stack.publisher.EventsForSession("pub-destroy"))
	if countAfter != countBefore {
		t.Fatalf("unexpected events after destroy: before=%d after=%d", countBefore, countAfter)
	}
}

func TestTurnScopedReceiverEndsAtTurnCompleted(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "turn-scope"); err != nil {
		t.Fatalf("create: %v", err)
	}
	recv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "turn-scope",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	evts := collectAllEvents(t, recv)
	if len(evts) == 0 {
		t.Fatal("expected turn events")
	}
	last := evts[len(evts)-1]
	if last.Type != agentapi.EventTurnCompleted {
		t.Fatalf("last event type = %s, want %s", last.Type, agentapi.EventTurnCompleted)
	}
	if _, err := recv.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after terminal event, got %v", err)
	}
}

func TestSubscribeEventsMultipleSubscribers(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "multi-sub"); err != nil {
		t.Fatalf("create: %v", err)
	}

	sub1, err := stack.svc.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{SessionID: "multi-sub"})
	if err != nil {
		t.Fatalf("sub1: %v", err)
	}
	sub2, err := stack.svc.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{SessionID: "multi-sub"})
	if err != nil {
		t.Fatalf("sub2: %v", err)
	}

	turnRecv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "multi-sub",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = collectAllEvents(t, turnRecv)

	evts1 := collectEventsWithTimeout(t, sub1, 200*time.Millisecond)
	evts2 := collectEventsWithTimeout(t, sub2, 200*time.Millisecond)
	if len(evts1) == 0 || len(evts2) == 0 {
		t.Fatalf("expected non-empty subscriber streams, got %d and %d", len(evts1), len(evts2))
	}
	if len(evts1) != len(evts2) {
		t.Fatalf("subscriber event count mismatch: %d vs %d", len(evts1), len(evts2))
	}
	for i := range evts1 {
		if evts1[i].Type != evts2[i].Type {
			t.Fatalf("event type mismatch at %d: %s vs %s", i, evts1[i].Type, evts2[i].Type)
		}
	}
}

func TestDualDeliveryBothStreamsReceiveEvents(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "dual"); err != nil {
		t.Fatalf("create: %v", err)
	}
	sessionSub, err := stack.svc.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{SessionID: "dual"})
	if err != nil {
		t.Fatalf("subscribe events: %v", err)
	}
	turnRecv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "dual",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	turnEvents := collectAllEvents(t, turnRecv)
	sessionEvents := collectEventsWithTimeout(t, sessionSub, 200*time.Millisecond)
	if len(sessionEvents) < len(turnEvents) {
		t.Fatalf("session events shorter than turn events: %d < %d", len(sessionEvents), len(turnEvents))
	}
	for _, turnEvent := range turnEvents {
		found := false
		for _, sessionEvent := range sessionEvents {
			if turnEvent.Type == sessionEvent.Type {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("turn event %s not found in session stream", turnEvent.Type)
		}
	}
}

func TestDV1_StructuredAndTurnStreamsIndependent(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "dual-view"); err != nil {
		t.Fatalf("create: %v", err)
	}

	sub, err := stack.svc.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{SessionID: "dual-view"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	firstTurn, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "dual-view",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send first: %v", err)
	}

	if err := sub.Close(); err != nil {
		t.Fatalf("close subscriber: %v", err)
	}

	turnEvents := collectAllEvents(t, firstTurn)
	if len(turnEvents) == 0 {
		t.Fatalf("expected turn events")
	}
	turnCompleted := false
	for _, evt := range turnEvents {
		if evt.Type == agentapi.EventTurnCompleted {
			turnCompleted = true
			break
		}
	}
	if !turnCompleted {
		t.Fatalf("expected turn completed after closing session stream")
	}

	getResp, err := stack.svc.GetSession(context.Background(), &agentapi.GetAgentSessionRequest{SessionID: "dual-view"})
	if err != nil {
		t.Fatalf("get session after subscriber close: %v", err)
	}
	if getResp.SessionID != "dual-view" {
		t.Fatalf("session id = %q, want dual-view", getResp.SessionID)
	}
	independentSub, err := stack.svc.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{
		SessionID: "dual-view",
	})
	if err != nil {
		t.Fatalf("resubscribe after close: %v", err)
	}
	defer independentSub.Close()
	evts := collectEventsWithTimeout(t, independentSub, 100*time.Millisecond)
	if len(evts) == 0 {
		t.Fatalf("expected at least initial state event on resubscribe")
	}
	if evts[0].SessionID != "dual-view" {
		t.Fatalf("resubscribe event session = %q, want dual-view", evts[0].SessionID)
	}
}

func TestDV2_EventStreamSurvivesSubscriberFailure(t *testing.T) {
	stack := newServiceTestStack(t)
	if _, err := stack.createSession(context.Background(), "survivor"); err != nil {
		t.Fatalf("create: %v", err)
	}

	sub, err := stack.svc.SubscribeEvents(context.Background(), &agentapi.SubscribeEventsRequest{SessionID: "survivor"})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_ = sub.Close()

	recv, err := stack.svc.SendMessage(context.Background(), &agentapi.SendMessageRequest{
		SessionID: "survivor",
		Message:   "hello",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	events := collectAllEvents(t, recv)
	if len(events) == 0 {
		t.Fatalf("expected turn events")
	}

	pubEvents := stack.publisher.EventsForSession("survivor")
	if len(pubEvents) == 0 {
		t.Fatalf("expected publisher events after subscriber failure")
	}
}

func TestGS2_ForceShutdownAfterDeadline(t *testing.T) {
	stack := newServiceTestStack(t, WithCloseDrainTimeout(25*time.Millisecond))

	// Simulate a leaked in-flight turn to force the timeout path.
	stack.svc.wg.Add(1)
	defer stack.svc.wg.Done()

	start := time.Now()
	err := stack.svc.Close()
	if err == nil {
		t.Fatalf("expected close deadline exceeded error")
	}
	assertRPCCode(t, err, CodeDeadlineExceeded)
	if elapsed := time.Since(start); elapsed < 25*time.Millisecond {
		t.Fatalf("close returned before drain deadline: %s", elapsed)
	}
}

type serviceTestStack struct {
	svc       *AgentLoopService
	publisher *mockEventPublisher
	factory   *mockDriverFactory
	provider  string
	model     string
}

func newServiceTestStack(t *testing.T, opts ...ServiceOption) *serviceTestStack {
	t.Helper()
	provider, model := pickAnyRegisteredModel(t)
	publisher := &mockEventPublisher{}
	factory := newMockDriverFactory()

	svc := NewAgentLoopService(publisher, opts...)
	svc.SetDriverFactory(factory.Create)

	t.Cleanup(func() {
		_ = svc.Close()
	})

	return &serviceTestStack{
		svc:       svc,
		publisher: publisher,
		factory:   factory,
		provider:  provider,
		model:     model,
	}
}

func (s *serviceTestStack) baseConfig(sessionID string) agentapi.SessionConfig {
	return agentapi.SessionConfig{
		SessionID: sessionID,
		Driver:    "mock",
		Model:     s.model,
		Provider:  s.provider,
		Tools:     []string{"bash", "read_file"},
		Metadata: map[string]any{
			"team": "runtime",
		},
		ToolEnvironment: agentapi.ToolEnvironmentConfig{
			Type: agentapi.ToolEnvLocal,
		},
	}
}

func (s *serviceTestStack) createSession(ctx context.Context, sessionID string) (*agentapi.CreateAgentSessionResponse, error) {
	return s.svc.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
		SessionConfig: s.baseConfig(sessionID),
	})
}

type mockDriverFactory struct {
	mu      sync.Mutex
	drivers map[string]*mockAgentDriver
}

func newMockDriverFactory() *mockDriverFactory {
	return &mockDriverFactory{drivers: make(map[string]*mockAgentDriver)}
}

func (f *mockDriverFactory) SetDriver(sessionID string, driver *mockAgentDriver) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drivers[sessionID] = driver
}

func (f *mockDriverFactory) Create(name string, cfg DriverConfig) (AgentDriver, error) {
	if name != "mock" {
		return nil, fmt.Errorf("unknown driver: %s", name)
	}
	sessionID, _ := cfg.Metadata["session_id"].(string)
	f.mu.Lock()
	defer f.mu.Unlock()
	if driver, ok := f.drivers[sessionID]; ok {
		return driver, nil
	}
	if driver, ok := f.drivers["default"]; ok {
		return driver, nil
	}
	driver := &mockAgentDriver{}
	f.drivers[sessionID] = driver
	return driver, nil
}

type mockEventPublisher struct {
	mu     sync.Mutex
	events map[string][]AgentEvent
}

func (p *mockEventPublisher) Publish(sessionID string, event AgentEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.events == nil {
		p.events = make(map[string][]AgentEvent)
	}
	p.events[sessionID] = append(p.events[sessionID], event)
}

func (p *mockEventPublisher) EventsForSession(sessionID string) []AgentEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	src := p.events[sessionID]
	out := make([]AgentEvent, len(src))
	copy(out, src)
	return out
}

func collectAllEvents(t *testing.T, recv agentapi.EventReceiver) []agentapi.AgentEvent {
	t.Helper()
	var events []agentapi.AgentEvent
	for {
		evt, err := recv.Recv()
		if errors.Is(err, io.EOF) {
			return events
		}
		if err != nil {
			t.Fatalf("recv event: %v", err)
		}
		if evt != nil {
			events = append(events, *evt)
		}
	}
}

func collectEventsWithTimeout(t *testing.T, recv agentapi.EventReceiver, timeout time.Duration) []agentapi.AgentEvent {
	t.Helper()
	timer := time.AfterFunc(timeout, func() {
		_ = recv.Close()
	})
	defer timer.Stop()
	return collectAllEvents(t, recv)
}

func waitForCondition(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fn()
}

func assertRPCCode(t *testing.T, err error, code ServiceCode) {
	t.Helper()
	if err == nil {
		if code == "" {
			return
		}
		t.Fatalf("expected service code %s but got nil error", code)
	}
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected ServiceError, got %T (%v)", err, err)
	}
	if svcErr.Code != code {
		t.Fatalf("service code = %s, want %s (err=%v)", svcErr.Code, code, err)
	}
}

func pickAnyRegisteredModel(t *testing.T) (provider string, modelID string) {
	t.Helper()
	providers := ai.GetModelProviders()
	if len(providers) == 0 {
		t.Fatal("no model providers registered")
	}
	for _, p := range providers {
		models := ai.GetModels(p)
		if len(models) == 0 {
			continue
		}
		return p, models[0].ID
	}
	t.Fatal("no models registered")
	return "", ""
}

func containsEventType(events []AgentEvent, typ AgentEventType) bool {
	for _, evt := range events {
		if evt.Type == typ {
			return true
		}
	}
	return false
}

func eventTypes(events []AgentEvent) []AgentEventType {
	out := make([]AgentEventType, 0, len(events))
	for _, evt := range events {
		out = append(out, evt.Type)
	}
	return out
}

type mockAgentDriver struct {
	mu          sync.Mutex
	startCount  int
	stopCount   int
	startErr    error
	resumeErr   error
	subscribers []func(AgentEvent)

	TurnScript   []AgentEvent
	TurnDelay    time.Duration
	BlockOnStart chan struct{}
	PanicOnStart bool
}

var _ AgentDriver = (*mockAgentDriver)(nil)

func (d *mockAgentDriver) Start(ctx context.Context, sess *Session, prompt string) error {
	_ = sess
	_ = prompt
	d.mu.Lock()
	d.startCount++
	err := d.startErr
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if d.PanicOnStart {
		panic("mock driver panic")
	}
	go d.runTurn(ctx)
	return nil
}

func (d *mockAgentDriver) Resume(ctx context.Context, sess *Session) error {
	_ = sess
	d.mu.Lock()
	err := d.resumeErr
	d.mu.Unlock()
	if err != nil {
		return err
	}
	go d.runTurn(ctx)
	return nil
}

func (d *mockAgentDriver) Stop(ctx context.Context) error {
	_ = ctx
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopCount++
	return nil
}

func (d *mockAgentDriver) Subscribe(fn func(AgentEvent)) func() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subscribers = append(d.subscribers, fn)
	idx := len(d.subscribers) - 1
	return func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if idx >= 0 && idx < len(d.subscribers) {
			d.subscribers[idx] = nil
		}
	}
}

func (d *mockAgentDriver) StartCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.startCount
}

func (d *mockAgentDriver) runTurn(ctx context.Context) {
	if d.BlockOnStart != nil {
		select {
		case <-d.BlockOnStart:
		case <-ctx.Done():
			return
		}
	}

	script := d.turnScript()
	for _, evt := range script {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if d.TurnDelay > 0 {
			select {
			case <-time.After(d.TurnDelay):
			case <-ctx.Done():
				return
			}
		}
		d.emit(evt)
	}
}

func (d *mockAgentDriver) turnScript() []AgentEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.TurnScript) == 0 {
		return []AgentEvent{
			{Type: EventTurnStarted, Turn: 1},
			{Type: EventAgentMessageDelta, Turn: 1, Delta: "ok"},
			{Type: EventAgentMessageCompleted, Turn: 1},
			{Type: EventTurnCompleted, Turn: 1},
		}
	}
	out := make([]AgentEvent, len(d.TurnScript))
	copy(out, d.TurnScript)
	return out
}

func (d *mockAgentDriver) emit(evt AgentEvent) {
	d.mu.Lock()
	subs := make([]func(AgentEvent), 0, len(d.subscribers))
	for _, sub := range d.subscribers {
		if sub != nil {
			subs = append(subs, sub)
		}
	}
	d.mu.Unlock()
	for _, fn := range subs {
		fn(evt)
	}
}
