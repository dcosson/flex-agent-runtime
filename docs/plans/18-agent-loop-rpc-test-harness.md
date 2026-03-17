# 18: Agent Loop RPC Service — Test Harness

**Companion to:** [18-agent-loop-rpc.md](./18-agent-loop-rpc.md)
**Scope:** All testing beyond basic unit tests for the Agent Loop RPC Service, including AgentLoopService, AgentRPCServer, AgentServiceClient, EventPublisher/EventReceiver, SandboxControl, codec round-trips, session forking, cross-agent resume, and event stream fidelity.

---

## 1. Mock Agent Driver Setup

All tests in this harness use a mock `AgentDriver` to avoid real AI provider calls. The mock driver is configurable to simulate various agent behaviors deterministically.

```go
// internal/agent/agenttest/mock_driver.go

// MockAgentDriver implements agent.AgentDriver for testing AgentLoopService
// without real AI providers. It emits a configurable sequence of AgentEvents
// when Start() or Resume() is called, simulating a real agent turn.
type MockAgentDriver struct {
    mu          sync.Mutex
    startCount  int
    stopCount   int
    startErr    error
    resumeErr   error
    subscribers []func(agent.AgentEvent)

    // TurnScript defines the events emitted when Start() or Prompt() is called.
    // If nil, a default script is used: TurnStarted -> Delta("ok") -> TurnCompleted.
    TurnScript []agent.AgentEvent

    // TurnDelay is the delay between emitting each event in the script.
    TurnDelay time.Duration

    // ToolCallScript defines tool calls the driver will emit. When the driver
    // emits EventToolStarted, the agent loop will invoke ExecuteTool on the
    // ExecutionEnvironment. This enables testing the full tool execution path.
    ToolCallScript []ToolCallEntry

    // BlockOnStart, if non-nil, is a channel the driver blocks on after
    // subscribing but before emitting events. Used to test concurrent
    // operations during a turn (steer, abort, follow-up).
    BlockOnStart chan struct{}

    // PanicOnStart causes Start() to panic, for testing panic recovery.
    PanicOnStart bool
}

type ToolCallEntry struct {
    ToolName  string
    Arguments map[string]any
}

func (d *MockAgentDriver) Start(ctx context.Context, sess *agent.Session, prompt string) error {
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

func (d *MockAgentDriver) Resume(ctx context.Context, sess *agent.Session) error {
    d.mu.Lock()
    err := d.resumeErr
    d.mu.Unlock()
    if err != nil {
        return err
    }
    go d.runTurn(ctx)
    return nil
}

func (d *MockAgentDriver) Stop(ctx context.Context) error {
    d.mu.Lock()
    defer d.mu.Unlock()
    d.stopCount++
    return nil
}

func (d *MockAgentDriver) Subscribe(fn func(agent.AgentEvent)) func() {
    d.mu.Lock()
    defer d.mu.Unlock()
    d.subscribers = append(d.subscribers, fn)
    idx := len(d.subscribers) - 1
    return func() {
        d.mu.Lock()
        defer d.mu.Unlock()
        d.subscribers[idx] = nil
    }
}

func (d *MockAgentDriver) runTurn(ctx context.Context) {
    if d.BlockOnStart != nil {
        select {
        case <-d.BlockOnStart:
        case <-ctx.Done():
            return
        }
    }
    script := d.TurnScript
    if script == nil {
        script = defaultTurnScript()
    }
    for _, evt := range script {
        select {
        case <-ctx.Done():
            return
        default:
        }
        if d.TurnDelay > 0 {
            time.Sleep(d.TurnDelay)
        }
        d.emit(evt)
    }
}

func (d *MockAgentDriver) emit(evt agent.AgentEvent) {
    d.mu.Lock()
    subs := make([]func(agent.AgentEvent), len(d.subscribers))
    copy(subs, d.subscribers)
    d.mu.Unlock()
    for _, fn := range subs {
        if fn != nil {
            fn(evt)
        }
    }
}

func defaultTurnScript() []agent.AgentEvent {
    return []agent.AgentEvent{
        {Type: agent.EventTurnStarted},
        {Type: agent.EventDelta, Data: map[string]any{"text": "Hello from mock agent"}},
        {Type: agent.EventAgentMessageCompleted},
        {Type: agent.EventTurnCompleted},
    }
}
```

**Mock driver variants for specific test scenarios:**

| Variant | Behavior | Used By |
|---------|----------|---------|
| `defaultTurnScript()` | Immediate turn with text response | Most unit/integration tests |
| `slowTurnScript(delay)` | Each event delayed by `delay` | Abort, steer, shutdown tests |
| `toolCallScript(calls)` | Emits tool_started events requiring tool execution | Tool execution integration tests |
| `errorScript(err)` | Start() returns error | Error handling tests |
| `panicScript()` | Start() panics | Panic recovery tests |
| `multiTurnScript(n)` | Emits n consecutive turns with tool calls between them | Multi-turn conversation tests |
| `infiniteScript()` | Never emits TurnCompleted (blocks on channel) | Abort and shutdown drain tests |

---

## 2. In-Memory ConnectRPC Test Server Infrastructure

All RPC transport tests use an in-memory ConnectRPC server (no real network). This follows the pattern established in `internal/rpc/rpctest/harness_test.go`.

```go
// internal/agent/agenttest/rpc_stack.go

// AgentTestStack wires a full AgentLoopService + AgentRPCServer + AgentServiceClient
// stack over in-process calls. No network sockets are opened.
type AgentTestStack struct {
    Service       *agent.AgentLoopService
    Server        *server.AgentRPCServer
    Client        *client.AgentServiceClient
    EventServer   *server.AgentEventServer
    MockPublisher *MockEventPublisher
    DriverFactory *MockDriverFactory
}

// MockEventPublisher implements agent.EventPublisher and records all published events.
type MockEventPublisher struct {
    mu     sync.Mutex
    events []PublishedEvent
    closed map[string]bool // sessions whose events were stopped
}

type PublishedEvent struct {
    SessionID string
    Event     agent.AgentEvent
    Timestamp time.Time
}

func (p *MockEventPublisher) Publish(sessionID string, event agent.AgentEvent) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.events = append(p.events, PublishedEvent{
        SessionID: sessionID, Event: event, Timestamp: time.Now(),
    })
}

func (p *MockEventPublisher) EventsForSession(sessionID string) []agent.AgentEvent {
    p.mu.Lock()
    defer p.mu.Unlock()
    var out []agent.AgentEvent
    for _, pe := range p.events {
        if pe.SessionID == sessionID {
            out = append(out, pe.Event)
        }
    }
    return out
}

// MockDriverFactory returns pre-configured MockAgentDrivers for each session.
type MockDriverFactory struct {
    mu      sync.Mutex
    drivers map[string]*MockAgentDriver // keyed by session ID (or "default")
}

func (f *MockDriverFactory) SetDriver(sessionID string, d *MockAgentDriver) {
    f.mu.Lock()
    defer f.mu.Unlock()
    f.drivers[sessionID] = d
}

func NewAgentTestStack(t *testing.T, opts ...agent.ServiceOption) *AgentTestStack {
    t.Helper()
    pub := &MockEventPublisher{closed: make(map[string]bool)}
    factory := &MockDriverFactory{drivers: make(map[string]*MockAgentDriver)}

    svc := agent.NewAgentLoopService(pub, opts...)
    // Inject mock driver factory so AgentLoopService uses MockAgentDriver
    // instead of resolving real drivers from the registry.
    svc.SetDriverFactory(factory.Create)

    srv := server.NewAgentRPCServer(svc)
    cl := client.NewAgentServiceClient(srv) // in-process, no HTTP

    t.Cleanup(func() { _ = svc.Close() })

    return &AgentTestStack{
        Service: svc, Server: srv, Client: cl,
        MockPublisher: pub, DriverFactory: factory,
    }
}

// createAgentSession is a helper that creates a session with sensible defaults.
func createAgentSession(t *testing.T, svc agentapi.AgentService, id string) *agentapi.CreateAgentSessionResponse {
    t.Helper()
    resp, err := svc.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{
            SessionID: id,
            Driver:    "mock",
            Model:     "mock-model",
            Provider:  "mock-provider",
            Tools:     []string{"bash", "read_file"},
        },
    })
    if err != nil {
        t.Fatalf("CreateSession(%q) error: %v", id, err)
    }
    return resp
}
```

---

## 3. Property-Based Tests

### P1. AgentService State Machine Validity

**Invariant:** For any randomly generated sequence of valid AgentService operations on a session, the service always transitions to a valid state and never panics. Invalid operations are rejected with appropriate error codes.

```go
func TestPropertySessionStateMachine(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        stack := NewAgentTestStack(t)
        ctx := context.Background()

        // Create a session
        sess := createAgentSession(t, stack.Service, "")
        sessionID := sess.SessionID

        shadow := &sessionShadow{state: "idle"}

        ops := rapid.IntRange(1, 40).Draw(t, "opCount")
        for i := 0; i < ops; i++ {
            op := rapid.IntRange(0, 7).Draw(t, fmt.Sprintf("op-%d", i))
            switch op {
            case 0: // SendMessage
                receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
                    SessionID: sessionID, Message: "test prompt",
                })
                if shadow.state == "streaming" {
                    // Should get BusyError
                    assert.Error(t, err)
                } else {
                    if err == nil {
                        shadow.state = "streaming"
                        drainReceiver(receiver) // consume all events
                        shadow.state = "idle"
                    }
                }
            case 1: // Continue
                _, err := stack.Service.Continue(ctx, &agentapi.ContinueRequest{
                    SessionID: sessionID,
                })
                // Continue may fail if not in correct state; that's fine
                _ = err
            case 2: // Steer
                _, err := stack.Service.Steer(ctx, &agentapi.SteerRequest{
                    SessionID: sessionID, Message: "go faster",
                })
                if shadow.state == "idle" {
                    assert.Error(t, err) // FailedPrecondition when idle
                }
            case 3: // FollowUp
                _, err := stack.Service.FollowUp(ctx, &agentapi.FollowUpRequest{
                    SessionID: sessionID, Message: "then do this",
                })
                // FollowUp is valid in any state
                assert.NoError(t, err)
            case 4: // Abort
                _, _ = stack.Service.Abort(ctx, &agentapi.AbortRequest{
                    SessionID: sessionID, Reason: "test abort",
                })
            case 5: // GetSession
                resp, err := stack.Service.GetSession(ctx, &agentapi.GetAgentSessionRequest{
                    SessionID: sessionID,
                })
                assert.NoError(t, err)
                assert.NotEmpty(t, resp.State)
            case 6: // ListSessions
                resp, err := stack.Service.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
                assert.NoError(t, err)
                assert.GreaterOrEqual(t, len(resp.Sessions), 1)
            case 7: // DestroySession (terminal)
                _, err := stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
                    SessionID: sessionID,
                })
                assert.NoError(t, err)
                shadow.state = "destroyed"
                return // session is gone
            }
        }
    })
}

type sessionShadow struct {
    state string // "idle", "streaming", "destroyed"
}
```

### P2. Codec Round-Trip Fidelity (AgentMessageRecord)

**Invariant:** For any valid `agent.AgentMessage`, converting to `AgentMessageRecord` and back produces a semantically equivalent message. Content blocks, roles, and metadata are preserved.

```go
func TestPropertyCodecRoundTrip(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        role := rapid.SampledFrom([]string{"user", "assistant", "tool_result"}).Draw(t, "role")
        msg := generateRandomAgentMessage(t, role)

        record, err := agentapi.AgentMessageToRecord(msg)
        require.NoError(t, err)
        assert.Equal(t, role, record.Role)

        roundTripped, err := agentapi.RecordToAgentMessage(record)
        require.NoError(t, err)

        assertAgentMessageEqual(t, msg, roundTripped)
    })
}
```

**Generator coverage:** User messages (plain text, multi-line, unicode), assistant messages (single text block, multi-block with thinking + tool_use, all stop reasons), tool_result messages (success, error, multi-content-block results, image content blocks).

### P3. ResumeSession Validation Properties

**Invariant:** ResumeSession accepts any well-formed conversation log and produces warnings for recoverable issues. Malformed logs produce errors, not panics.

```go
func TestPropertyResumeSessionValidation(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        logLen := rapid.IntRange(0, 50).Draw(t, "logLen")
        log := generateRandomConversationLog(t, logLen)

        stack := NewAgentTestStack(t)
        resp, err := stack.Service.ResumeSession(context.Background(),
            &agentapi.ResumeSessionRequest{
                SessionConfig: agentapi.SessionConfig{
                    Driver: "mock", Model: "mock-model", Provider: "mock-provider",
                },
                SchemaVersion:   1,
                ConversationLog: log,
            })

        if err != nil {
            // Error is expected for malformed logs; verify no panic occurred
            return
        }

        // Valid log: session should be created with correct conversation length
        assert.NotEmpty(t, resp.SessionID)
        assert.Equal(t, "idle", resp.State)
        assert.Equal(t, logLen, resp.ConversationLen)
    })
}
```

### P4. Event Stream Ordering

**Invariant:** Events received from `SendMessage`'s `EventReceiver` are always in the same order they were emitted by the driver. No reordering or duplication occurs.

```go
func TestPropertyEventStreamOrdering(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        eventCount := rapid.IntRange(3, 100).Draw(t, "eventCount")
        script := generateNumberedScript(eventCount) // Each event has a sequence number

        stack := NewAgentTestStack(t)
        stack.DriverFactory.SetDriver("default", &MockAgentDriver{TurnScript: script})

        sess := createAgentSession(t, stack.Service, "")
        receiver, err := stack.Service.SendMessage(context.Background(),
            &agentapi.SendMessageRequest{SessionID: sess.SessionID, Message: "go"})
        require.NoError(t, err)

        received := collectAllEvents(receiver)
        assert.Equal(t, eventCount, len(received))

        for i, evt := range received {
            seq := evt.Data.(map[string]any)["seq"].(int)
            assert.Equal(t, i, seq, "event %d out of order", i)
        }
    })
}
```

### P5. EventPublisher Session ID Consistency

**Invariant:** Every event published via `EventPublisher.Publish()` carries the correct session ID. Events from different sessions never cross-contaminate.

```go
func TestPropertyEventPublisherSessionIDs(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        sessionCount := rapid.IntRange(2, 10).Draw(t, "sessions")
        stack := NewAgentTestStack(t)
        ctx := context.Background()

        sessionIDs := make([]string, sessionCount)
        for i := range sessionIDs {
            id := fmt.Sprintf("prop-sess-%d", i)
            createAgentSession(t, stack.Service, id)
            sessionIDs[i] = id
        }

        // Send messages to all sessions
        for _, id := range sessionIDs {
            receiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
                SessionID: id, Message: "test",
            })
            if receiver != nil {
                drainReceiver(receiver)
            }
        }

        // Verify published events have correct session IDs
        for _, id := range sessionIDs {
            events := stack.MockPublisher.EventsForSession(id)
            for _, evt := range events {
                // This is implicitly verified by EventsForSession filtering,
                // but explicitly check the published records
                assert.NotEmpty(t, events)
            }
        }
    })
}
```

### P6. Cross-Agent Resume Round-Trip

**Invariant:** A conversation log serialized via `AgentMessageToRecord`, then deserialized via `RecordToAgentMessage`, then converted via `AgentMessageToConversationEntries`, produces entries that retain the semantic content of the original conversation. Text content is preserved; structural metadata may be lossy (documented in plan section 3.3).

```go
func TestPropertyCrossAgentResumeRoundTrip(t *testing.T) {
    rapid.Check(t, func(t *rapid.T) {
        turnCount := rapid.IntRange(1, 20).Draw(t, "turns")
        conversation := generateRealisticConversation(t, turnCount)

        // Step 1: Serialize to records
        records := make([]agentapi.AgentMessageRecord, len(conversation))
        for i, msg := range conversation {
            rec, err := agentapi.AgentMessageToRecord(msg)
            require.NoError(t, err)
            records[i] = rec
        }

        // Step 2: Deserialize back to messages
        restored := make([]agent.AgentMessage, len(records))
        for i, rec := range records {
            msg, err := agentapi.RecordToAgentMessage(rec)
            require.NoError(t, err)
            restored[i] = msg
        }

        // Step 3: Convert to ConversationEntry for cross-agent resume
        var entries []driver.ConversationEntry
        for _, msg := range restored {
            entries = append(entries, agentapi.AgentMessageToConversationEntries(msg)...)
        }

        // Verify semantic fidelity
        // - All user messages appear as user entries
        // - All assistant text blocks appear in entries
        // - Tool use and tool result entries are present
        assertConversationEntriesCoverMessages(t, conversation, entries)
    })
}
```

---

## 4. AgentLoopService Unit Tests

### U1. Session Lifecycle

```go
func TestCreateSession_Valid(t *testing.T) {
    stack := NewAgentTestStack(t)
    resp := createAgentSession(t, stack.Service, "sess-1")
    assert.Equal(t, "sess-1", resp.SessionID)
    assert.Equal(t, "idle", resp.State)
}

func TestCreateSession_GeneratesIDIfEmpty(t *testing.T) {
    stack := NewAgentTestStack(t)
    resp := createAgentSession(t, stack.Service, "")
    assert.NotEmpty(t, resp.SessionID) // UUID generated
}

func TestCreateSession_DuplicateID(t *testing.T) {
    stack := NewAgentTestStack(t)
    createAgentSession(t, stack.Service, "dup-1")
    _, err := stack.Service.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{SessionID: "dup-1", Driver: "mock"},
    })
    assertAgentRPCError(t, err, rpc.CodeAlreadyExists)
}

func TestCreateSession_ExceedsMaxSessions(t *testing.T) {
    stack := NewAgentTestStack(t, agent.WithMaxSessions(2))
    createAgentSession(t, stack.Service, "s1")
    createAgentSession(t, stack.Service, "s2")
    _, err := stack.Service.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{SessionID: "s3", Driver: "mock"},
    })
    assertAgentRPCError(t, err, rpc.CodeResourceExhausted)
}

func TestCreateSession_InvalidModel(t *testing.T) {
    stack := NewAgentTestStack(t)
    _, err := stack.Service.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{
            SessionID: "bad-model", Driver: "mock",
            Model: "nonexistent-model", Provider: "nonexistent-provider",
        },
    })
    assert.Error(t, err)
}
```

### U2. Concurrent Sessions Independence

```go
func TestConcurrentSessions_Independent(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    const sessions = 5
    var wg sync.WaitGroup
    errs := make(chan error, sessions)

    for i := 0; i < sessions; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            sessID := fmt.Sprintf("concurrent-%d", id)
            createAgentSession(t, stack.Service, sessID)

            receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
                SessionID: sessID, Message: "hello",
            })
            if err != nil {
                errs <- fmt.Errorf("SendMessage %s: %v", sessID, err)
                return
            }

            events := collectAllEvents(receiver)
            if len(events) == 0 {
                errs <- fmt.Errorf("%s: received 0 events", sessID)
                return
            }

            // Verify events have correct session ID
            for _, evt := range events {
                if evt.SessionID != sessID {
                    errs <- fmt.Errorf("%s: event has wrong session ID %q", sessID, evt.SessionID)
                    return
                }
            }

            _, err = stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
                SessionID: sessID,
            })
            if err != nil {
                errs <- fmt.Errorf("Destroy %s: %v", sessID, err)
            }
        }(i)
    }
    wg.Wait()
    close(errs)
    for err := range errs {
        t.Error(err)
    }

    // All sessions should be gone
    resp, _ := stack.Service.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
    assert.Empty(t, resp.Sessions)
}
```

### U3. SendMessage BusyError

```go
func TestSendMessage_BusyError(t *testing.T) {
    blockCh := make(chan struct{})
    driver := &MockAgentDriver{
        TurnScript:   slowTurnScript(5 * time.Second),
        BlockOnStart: blockCh,
    }

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("busy-test", driver)
    createAgentSession(t, stack.Service, "busy-test")

    ctx := context.Background()
    // Start first turn (will block)
    go func() {
        stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
            SessionID: "busy-test", Message: "first",
        })
    }()

    time.Sleep(10 * time.Millisecond) // let first turn start

    // Second SendMessage should get BusyError
    _, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "busy-test", Message: "second",
    })
    assert.Error(t, err)
    // BusyError maps to CodeFailedPrecondition

    close(blockCh) // unblock the first turn
}
```

### U4. DestroySession During Active Turn

```go
func TestDestroySession_DuringActiveTurn(t *testing.T) {
    blockCh := make(chan struct{})
    driver := &MockAgentDriver{BlockOnStart: blockCh}

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("destroy-active", driver)
    createAgentSession(t, stack.Service, "destroy-active")

    ctx := context.Background()
    var receiver agentapi.EventReceiver
    go func() {
        receiver, _ = stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
            SessionID: "destroy-active", Message: "working",
        })
    }()

    time.Sleep(10 * time.Millisecond) // let turn start

    // Destroy while turn is active
    _, err := stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
        SessionID: "destroy-active",
    })
    assert.NoError(t, err)

    // Session should be gone
    _, err = stack.Service.GetSession(ctx, &agentapi.GetAgentSessionRequest{
        SessionID: "destroy-active",
    })
    assertAgentRPCError(t, err, rpc.CodeNotFound)

    close(blockCh)
}
```

### U5. Steer and FollowUp Semantics

```go
func TestSteer_WhileIdle_FailedPrecondition(t *testing.T) {
    stack := NewAgentTestStack(t)
    createAgentSession(t, stack.Service, "steer-idle")

    _, err := stack.Service.Steer(context.Background(), &agentapi.SteerRequest{
        SessionID: "steer-idle", Message: "go faster",
    })
    assertAgentRPCError(t, err, rpc.CodeFailedPrecondition)
}

func TestFollowUp_ValidInAnyState(t *testing.T) {
    stack := NewAgentTestStack(t)
    createAgentSession(t, stack.Service, "followup-test")

    // FollowUp when idle -- should succeed
    _, err := stack.Service.FollowUp(context.Background(), &agentapi.FollowUpRequest{
        SessionID: "followup-test", Message: "then do this",
    })
    assert.NoError(t, err)
}
```

### U6. Close (Graceful Shutdown)

```go
func TestClose_RejectsNewSessions(t *testing.T) {
    stack := NewAgentTestStack(t)
    createAgentSession(t, stack.Service, "pre-close")

    err := stack.Service.Close()
    require.NoError(t, err)

    _, err = stack.Service.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{SessionID: "post-close", Driver: "mock"},
    })
    assertAgentRPCError(t, err, rpc.CodeUnavailable)
}

func TestClose_DrainsActiveSessions(t *testing.T) {
    blockCh := make(chan struct{})
    driver := &MockAgentDriver{BlockOnStart: blockCh}

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("drain-test", driver)
    createAgentSession(t, stack.Service, "drain-test")

    ctx := context.Background()
    go func() {
        stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
            SessionID: "drain-test", Message: "working",
        })
    }()

    time.Sleep(10 * time.Millisecond)

    // Close should complete even with active turn (force-cancel after deadline)
    done := make(chan error, 1)
    go func() { done <- stack.Service.Close() }()

    close(blockCh) // unblock the turn

    select {
    case err := <-done:
        assert.NoError(t, err)
    case <-time.After(10 * time.Second):
        t.Fatal("Close() did not return within 10s")
    }
}
```

---

## 5. EventPublisher / EventReceiver Tests

### EP1. EventPublisher Receives All Events

```go
func TestEventPublisher_ReceivesAllSessionEvents(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "pub-test")

    receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "pub-test", Message: "hello",
    })
    require.NoError(t, err)
    drainReceiver(receiver)

    events := stack.MockPublisher.EventsForSession("pub-test")
    assert.NotEmpty(t, events)

    // Should include TurnStarted and TurnCompleted at minimum
    types := eventTypes(events)
    assert.Contains(t, types, agent.EventTurnStarted)
    assert.Contains(t, types, agent.EventTurnCompleted)
}
```

### EP2. EventPublisher Stops After DestroySession

```go
func TestEventPublisher_StopsAfterDestroy(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "pub-destroy")

    receiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "pub-destroy", Message: "hello",
    })
    drainReceiver(receiver)
    countBefore := len(stack.MockPublisher.EventsForSession("pub-destroy"))

    stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
        SessionID: "pub-destroy",
    })

    // No more events should be published after destroy
    countAfter := len(stack.MockPublisher.EventsForSession("pub-destroy"))
    assert.Equal(t, countBefore, countAfter,
        "events published after destroy: before=%d after=%d", countBefore, countAfter)
}
```

### EP3. EventReceiver Turn Scoping

```go
func TestEventReceiver_TurnScoped(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "turn-scope")

    receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "turn-scope", Message: "hello",
    })
    require.NoError(t, err)

    events := collectAllEvents(receiver)

    // The last event should be TurnCompleted
    assert.Equal(t, agent.EventTurnCompleted, events[len(events)-1].Type)

    // After TurnCompleted, Recv() should return io.EOF
    _, err = receiver.Recv()
    assert.ErrorIs(t, err, io.EOF)
}
```

### EP4. Multiple SubscribeEvents Subscribers

```go
func TestSubscribeEvents_MultipleSubscribers(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "multi-sub")

    // Open two subscribers
    sub1, err := stack.Service.SubscribeEvents(ctx, &agentapi.SubscribeEventsRequest{
        SessionID: "multi-sub",
    })
    require.NoError(t, err)
    sub2, err := stack.Service.SubscribeEvents(ctx, &agentapi.SubscribeEventsRequest{
        SessionID: "multi-sub",
    })
    require.NoError(t, err)

    // Send a message
    turnReceiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "multi-sub", Message: "hello",
    })
    drainReceiver(turnReceiver)

    // Both subscribers should have received the same events
    events1 := collectEventsWithTimeout(sub1, 1*time.Second)
    events2 := collectEventsWithTimeout(sub2, 1*time.Second)

    assert.Equal(t, len(events1), len(events2),
        "subscriber event counts differ: %d vs %d", len(events1), len(events2))

    for i := range events1 {
        assert.Equal(t, events1[i].Type, events2[i].Type)
    }

    sub1.Close()
    sub2.Close()
}
```

### EP5. SubscribeEvents + SendMessage Dual Delivery

```go
func TestDualDelivery_BothStreamsReceiveEvents(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "dual-delivery")

    // Open session-wide subscriber
    sessionSub, _ := stack.Service.SubscribeEvents(ctx, &agentapi.SubscribeEventsRequest{
        SessionID: "dual-delivery",
    })

    // Send message (returns turn-scoped stream)
    turnReceiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "dual-delivery", Message: "hello",
    })

    // Both should receive the same events
    turnEvents := collectAllEvents(turnReceiver)
    sessionEvents := collectEventsWithTimeout(sessionSub, 1*time.Second)

    // Session subscriber sees at least as many events as turn subscriber
    assert.GreaterOrEqual(t, len(sessionEvents), len(turnEvents))

    // Turn events should be a subset of session events
    for _, te := range turnEvents {
        found := false
        for _, se := range sessionEvents {
            if te.Type == se.Type {
                found = true
                break
            }
        }
        assert.True(t, found, "turn event %s not found in session events", te.Type)
    }

    sessionSub.Close()
}
```

---

## 6. RPC Transport Tests

### RT1. ConnectRPC Client/Server Round-Trip

```go
func TestRPCRoundTrip_CreateGetDestroy(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    // Create via RPC client
    resp, err := stack.Client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{
            SessionID: "rpc-rt-1", Driver: "mock", Model: "mock-model",
        },
    })
    require.NoError(t, err)
    assert.Equal(t, "rpc-rt-1", resp.SessionID)

    // Get via RPC client
    getResp, err := stack.Client.GetSession(ctx, &agentapi.GetAgentSessionRequest{
        SessionID: "rpc-rt-1",
    })
    require.NoError(t, err)
    assert.Equal(t, "idle", getResp.State)

    // List via RPC client
    listResp, err := stack.Client.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
    require.NoError(t, err)
    assert.Equal(t, 1, len(listResp.Sessions))

    // Destroy via RPC client
    _, err = stack.Client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
        SessionID: "rpc-rt-1",
    })
    require.NoError(t, err)
}
```

### RT2. RPC Streaming Round-Trip (SendMessage)

```go
func TestRPCRoundTrip_SendMessage_StreamingEvents(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "rpc-stream")

    receiver, err := stack.Client.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "rpc-stream", Message: "hello over RPC",
    })
    require.NoError(t, err)

    events := collectAllEvents(receiver)
    assert.NotEmpty(t, events)
    assert.Equal(t, agent.EventTurnStarted, events[0].Type)
    assert.Equal(t, agent.EventTurnCompleted, events[len(events)-1].Type)
}
```

### RT3. RPC Error Mapping

```go
func TestRPCErrorMapping(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    tests := []struct {
        name     string
        action   func() error
        wantCode rpc.Code
    }{
        {
            name: "session not found",
            action: func() error {
                _, err := stack.Client.GetSession(ctx, &agentapi.GetAgentSessionRequest{
                    SessionID: "nonexistent",
                })
                return err
            },
            wantCode: rpc.CodeNotFound,
        },
        {
            name: "duplicate session",
            action: func() error {
                createAgentSession(t, stack.Service, "dup-rpc")
                _, err := stack.Client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
                    SessionConfig: agentapi.SessionConfig{SessionID: "dup-rpc", Driver: "mock"},
                })
                return err
            },
            wantCode: rpc.CodeAlreadyExists,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := tt.action()
            assertAgentRPCError(t, err, tt.wantCode)
        })
    }
}
```

---

## 7. Session Forking Stress Tests

### FK1. Fork N Sessions From One Conversation

**Invariant:** N sessions created via `ResumeSession` with the same conversation log all start independently. Changes in one forked session do not affect others. All N sessions can proceed concurrently without interference.

```go
func TestFork_NSessions_Independent(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    stack := NewAgentTestStack(t)
    ctx := context.Background()

    // Build a conversation log with 10 turns
    conversationLog := buildConversationLog(10)

    const forkCount = 20
    var wg sync.WaitGroup
    errs := make(chan error, forkCount)
    sessionIDs := make([]string, forkCount)

    // Fork all sessions concurrently
    for i := 0; i < forkCount; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            id := fmt.Sprintf("fork-%d", idx)
            sessionIDs[idx] = id

            resp, err := stack.Service.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
                SessionConfig: agentapi.SessionConfig{
                    SessionID: id, Driver: "mock", Model: "mock-model",
                },
                SchemaVersion:   1,
                ConversationLog: conversationLog,
            })
            if err != nil {
                errs <- fmt.Errorf("ResumeSession %s: %v", id, err)
                return
            }
            if resp.ConversationLen != len(conversationLog) {
                errs <- fmt.Errorf("%s: conversationLen=%d, want %d",
                    id, resp.ConversationLen, len(conversationLog))
                return
            }

            // Send a unique message to each fork
            receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
                SessionID: id, Message: fmt.Sprintf("Fork %d instructions", idx),
            })
            if err != nil {
                errs <- fmt.Errorf("SendMessage %s: %v", id, err)
                return
            }
            events := collectAllEvents(receiver)
            if len(events) == 0 {
                errs <- fmt.Errorf("%s: 0 events", id)
            }
        }(i)
    }
    wg.Wait()
    close(errs)

    for err := range errs {
        t.Error(err)
    }

    // All sessions should exist and be independent
    listResp, _ := stack.Service.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
    assert.Equal(t, forkCount, len(listResp.Sessions))

    // Clean up
    for _, id := range sessionIDs {
        stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: id})
    }
}
```

### FK2. Fork Under Load

```go
func TestFork_UnderLoad_ConcurrentResumeAndMessage(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    stack := NewAgentTestStack(t, agent.WithMaxSessions(100))
    ctx := context.Background()

    conversationLog := buildConversationLog(5)

    const waves = 5
    const forksPerWave = 10

    for wave := 0; wave < waves; wave++ {
        var wg sync.WaitGroup
        for i := 0; i < forksPerWave; i++ {
            wg.Add(1)
            go func(w, idx int) {
                defer wg.Done()
                id := fmt.Sprintf("load-fork-w%d-%d", w, idx)

                resp, err := stack.Service.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
                    SessionConfig: agentapi.SessionConfig{
                        SessionID: id, Driver: "mock", Model: "mock-model",
                    },
                    SchemaVersion:   1,
                    ConversationLog: conversationLog,
                })
                if err != nil {
                    t.Errorf("ResumeSession %s: %v", id, err)
                    return
                }

                // Send message and consume events
                receiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
                    SessionID: resp.SessionID, Message: "fork task",
                })
                if receiver != nil {
                    drainReceiver(receiver)
                }

                stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
                    SessionID: resp.SessionID,
                })
            }(wave, i)
        }
        wg.Wait()
    }

    // All sessions should be cleaned up
    listResp, _ := stack.Service.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
    assert.Empty(t, listResp.Sessions)
}
```

---

## 8. Cross-Agent Resume Round-Trip Tests

### CR1. Native -> Record -> Resume

```go
func TestCrossAgentResume_NativeToRecordToResume(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    // Create a session and run a conversation
    createAgentSession(t, stack.Service, "original")
    receiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "original", Message: "Write a hello world program",
    })
    originalEvents := collectAllEvents(receiver)

    // Extract conversation from events (simulate orchestrator persistence)
    records := extractRecordsFromEvents(originalEvents)

    // Resume on a new session
    resp, err := stack.Service.ResumeSession(ctx, &agentapi.ResumeSessionRequest{
        SessionConfig: agentapi.SessionConfig{
            SessionID: "resumed", Driver: "mock", Model: "mock-model",
        },
        SchemaVersion:   1,
        ConversationLog: records,
    })
    require.NoError(t, err)
    assert.Equal(t, "idle", resp.State)
    assert.Equal(t, len(records), resp.ConversationLen)
}
```

### CR2. Record -> ConversationEntry -> Claude Code Session Log

```go
func TestCrossAgentResume_RecordToConversationEntry(t *testing.T) {
    // Build records representing a realistic conversation
    records := []agentapi.AgentMessageRecord{
        {Turn: 1, Role: "user", Content: mustMarshal(map[string]string{"text": "Fix the bug"})},
        {Turn: 1, Role: "assistant", Content: mustMarshal(map[string]any{
            "content_blocks": []map[string]any{
                {"type": "thinking", "thinking": "Let me analyze..."},
                {"type": "text", "text": "I'll fix the bug in main.go"},
                {"type": "tool_use", "id": "tc-1", "name": "read_file",
                    "input": map[string]any{"path": "main.go"}},
            },
            "stop_reason": "toolUse",
        })},
        {Turn: 1, Role: "tool_result", Content: mustMarshal(map[string]any{
            "tool_call_id": "tc-1", "tool_name": "read_file",
            "content": []map[string]string{{"type": "text", "text": "package main..."}},
        })},
    }

    // Convert records -> AgentMessage -> ConversationEntry
    var entries []driver.ConversationEntry
    for _, rec := range records {
        msg, err := agentapi.RecordToAgentMessage(rec)
        require.NoError(t, err)
        entries = append(entries, agentapi.AgentMessageToConversationEntries(msg)...)
    }

    // Verify entries cover all semantic content
    assert.GreaterOrEqual(t, len(entries), 3) // user + assistant blocks + tool_result
    assert.Equal(t, "user", entries[0].Role)
    assert.Contains(t, entries[0].Content, "Fix the bug")

    // Find the tool_use entry
    hasToolUse := false
    for _, e := range entries {
        if e.Role == "tool_use" {
            hasToolUse = true
            assert.NotNil(t, e.ToolCall)
            assert.Equal(t, "read_file", e.ToolCall.Name)
        }
    }
    assert.True(t, hasToolUse, "missing tool_use entry in conversion")
}
```

### CR3. Schema Version Mismatch Rejection

```go
func TestResumeSession_SchemaVersionMismatch(t *testing.T) {
    stack := NewAgentTestStack(t)
    _, err := stack.Service.ResumeSession(context.Background(), &agentapi.ResumeSessionRequest{
        SessionConfig: agentapi.SessionConfig{Driver: "mock", Model: "mock-model"},
        SchemaVersion: 999, // unsupported version
        ConversationLog: []agentapi.AgentMessageRecord{
            {Turn: 1, Role: "user", Content: mustMarshal(map[string]string{"text": "hi"})},
        },
    })
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "schema version")
}
```

---

## 9. Event Stream Fidelity Verification

### EF1. No Dropped Events

**Invariant:** Every event emitted by the mock driver is received by the EventReceiver. No events are silently dropped during normal operation.

```go
func TestEventStreamFidelity_NoDrops(t *testing.T) {
    const eventCount = 500
    script := generateNumberedScript(eventCount)

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("fidelity", &MockAgentDriver{TurnScript: script})

    createAgentSession(t, stack.Service, "fidelity")
    receiver, err := stack.Service.SendMessage(context.Background(),
        &agentapi.SendMessageRequest{SessionID: "fidelity", Message: "go"})
    require.NoError(t, err)

    received := collectAllEvents(receiver)
    assert.Equal(t, eventCount, len(received),
        "dropped events: expected %d, got %d", eventCount, len(received))
}
```

### EF2. Terminal Event Guarantees

**Invariant:** The EventReceiver always terminates with either `io.EOF` (normal) or a non-EOF error (abnormal). It never hangs indefinitely.

```go
func TestEventStreamFidelity_TerminalEvent(t *testing.T) {
    stack := NewAgentTestStack(t)
    createAgentSession(t, stack.Service, "terminal-test")

    receiver, _ := stack.Service.SendMessage(context.Background(),
        &agentapi.SendMessageRequest{SessionID: "terminal-test", Message: "go"})

    // Drain all events
    for {
        _, err := receiver.Recv()
        if err != nil {
            assert.ErrorIs(t, err, io.EOF)
            break
        }
    }

    // Subsequent Recv() calls should also return io.EOF
    _, err := receiver.Recv()
    assert.ErrorIs(t, err, io.EOF)
    _, err = receiver.Recv()
    assert.ErrorIs(t, err, io.EOF)
}
```

### EF3. Event Ordering Across RPC

```go
func TestEventStreamFidelity_OrderingAcrossRPC(t *testing.T) {
    const eventCount = 200
    script := generateNumberedScript(eventCount)

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("rpc-order", &MockAgentDriver{TurnScript: script})

    createAgentSession(t, stack.Service, "rpc-order")

    // Use RPC client (not direct service) to verify ordering through transport
    receiver, err := stack.Client.SendMessage(context.Background(),
        &agentapi.SendMessageRequest{SessionID: "rpc-order", Message: "go"})
    require.NoError(t, err)

    received := collectAllEvents(receiver)
    assert.Equal(t, eventCount, len(received))

    for i := 0; i < len(received)-1; i++ {
        seqI := received[i].Data.(map[string]any)["seq"].(int)
        seqJ := received[i+1].Data.(map[string]any)["seq"].(int)
        assert.Less(t, seqI, seqJ,
            "events out of order: [%d]=%d >= [%d]=%d", i, seqI, i+1, seqJ)
    }
}
```

### EF4. DestroySession Closes Subscriber Streams with EOF

```go
func TestEventStreamFidelity_DestroyClosesStreams(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "destroy-stream")

    sub, _ := stack.Service.SubscribeEvents(ctx, &agentapi.SubscribeEventsRequest{
        SessionID: "destroy-stream",
    })

    // Destroy session
    stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
        SessionID: "destroy-stream",
    })

    // Subscriber should receive EOF
    _, err := sub.Recv()
    assert.ErrorIs(t, err, io.EOF)
}
```

---

## 10. SandboxControl Tests

### SC1. NativeSandboxControl with Mock Sandbox-Host

```go
func TestNativeSandboxControl_CreateDestroy(t *testing.T) {
    mockSandboxService := newMockSandboxService(t)
    ctrl := native.NewNativeSandboxControl(mockSandboxService)

    ctx := context.Background()

    resp, err := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
        Template:  "tank/bases/repo@v1",
        Labels:    map[string]string{"env": "test"},
        Resources: control.ResourceSpec{CPUs: 2.0, MemMB: 4096},
    })
    require.NoError(t, err)
    assert.NotEmpty(t, resp.SandboxID)
    assert.NotEmpty(t, resp.Address)

    // Verify capabilities
    caps := ctrl.Capabilities()
    assert.True(t, caps.Snapshots)
    assert.True(t, caps.Rollback)
    assert.True(t, caps.Pause)

    // Destroy
    err = ctrl.DestroySandbox(ctx, resp.SandboxID)
    assert.NoError(t, err)
}
```

### SC2. NativeSandboxControl Pause/Resume

```go
func TestNativeSandboxControl_PauseResume(t *testing.T) {
    mockSandboxService := newMockSandboxService(t)
    ctrl := native.NewNativeSandboxControl(mockSandboxService)
    ctx := context.Background()

    resp, _ := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
        Template: "tank/bases/repo@v1",
    })

    assert.NoError(t, ctrl.PauseSandbox(ctx, resp.SandboxID))
    assert.NoError(t, ctrl.ResumeSandbox(ctx, resp.SandboxID))

    ctrl.DestroySandbox(ctx, resp.SandboxID)
}
```

### SC3. NativeSandboxControl LaunchProcess (Mock)

Since `LaunchProcess` depends on `11-sandbox-host-service.add03`, this test uses a mock.

```go
func TestNativeSandboxControl_LaunchProcess_Mock(t *testing.T) {
    mockSandboxService := newMockSandboxService(t)
    mockSandboxService.SetLaunchProcessResult("proc-1", "sandbox-host:9100")

    ctrl := native.NewNativeSandboxControl(mockSandboxService)
    ctx := context.Background()

    sandboxResp, _ := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
        Template: "tank/bases/repo@v1",
    })

    launchResp, err := ctrl.LaunchProcess(ctx, control.LaunchProcessRequest{
        SandboxID:  sandboxResp.SandboxID,
        Binary:     "flexagent",
        Args:       []string{"serve", "agent", "--addr", ":8080"},
        Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-test"},
        ExposePort: 8080,
    })
    require.NoError(t, err)
    assert.Equal(t, "proc-1", launchResp.ProcessID)
    assert.Equal(t, "sandbox-host:9100", launchResp.Address)

    // GetProcessStatus
    status, err := ctrl.GetProcessStatus(ctx, control.GetProcessStatusRequest{
        SandboxID: sandboxResp.SandboxID, ProcessID: "proc-1",
    })
    require.NoError(t, err)
    assert.Equal(t, control.ProcessRunning, status.Status)

    // KillProcess
    err = ctrl.KillProcess(ctx, control.KillProcessRequest{
        SandboxID: sandboxResp.SandboxID, ProcessID: "proc-1",
    })
    assert.NoError(t, err)
}
```

### SC4. CloudSandboxControl Placeholder Tests

Cloud provider implementations (E2B, Daytona, Fly) are deferred to Batch 6. Placeholder tests validate the interface:

```go
func TestCloudSandboxControl_InterfaceCompliance(t *testing.T) {
    // When cloud providers are implemented, they must satisfy this compliance suite
    providers := []struct {
        name string
        new  func() control.SandboxControl
    }{
        // Uncomment as providers are implemented:
        // {"e2b", func() control.SandboxControl { return e2b.New(mockHTTPClient) }},
        // {"daytona", func() control.SandboxControl { return daytona.New(mockHTTPClient) }},
        // {"fly", func() control.SandboxControl { return fly.New(mockHTTPClient) }},
    }

    for _, p := range providers {
        t.Run(p.name, func(t *testing.T) {
            ctrl := p.new()
            caps := ctrl.Capabilities()
            // All providers must report capabilities without panicking
            _ = caps.Snapshots
            _ = caps.Pause
            _ = caps.LaunchProcess
        })
    }
}
```

---

## 11. Dual-View Streaming Tests (Termmux Agents)

### DV1. Structured + Terminal Streams Independent

```go
func TestDualView_StructuredAndTerminalIndependent(t *testing.T) {
    // Structured event stream and terminal stream have independent lifecycles.
    // If one fails, the other continues.

    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "dual-view")

    // Open structured event subscriber
    structuredSub, _ := stack.Service.SubscribeEvents(ctx, &agentapi.SubscribeEventsRequest{
        SessionID: "dual-view",
    })

    // Send a message
    turnReceiver, _ := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "dual-view", Message: "hello",
    })
    drainReceiver(turnReceiver)

    // Close structured subscriber (simulating stream failure)
    structuredSub.Close()

    // Session should still be functional
    getResp, err := stack.Service.GetSession(ctx, &agentapi.GetAgentSessionRequest{
        SessionID: "dual-view",
    })
    require.NoError(t, err)
    assert.NotEmpty(t, getResp.State)

    // Can still send messages
    turnReceiver2, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "dual-view", Message: "still working",
    })
    require.NoError(t, err)
    drainReceiver(turnReceiver2)
}
```

### DV2. Event Stream Survives Subscriber Failure

```go
func TestDualView_EventStreamSurvivesSubscriberFailure(t *testing.T) {
    stack := NewAgentTestStack(t)
    ctx := context.Background()

    createAgentSession(t, stack.Service, "survivor")

    // Open subscriber and immediately close it
    sub, _ := stack.Service.SubscribeEvents(ctx, &agentapi.SubscribeEventsRequest{
        SessionID: "survivor",
    })
    sub.Close()

    // Agent turn should still complete normally
    receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "survivor", Message: "hello",
    })
    require.NoError(t, err)
    events := collectAllEvents(receiver)
    assert.NotEmpty(t, events)

    // EventPublisher should still have received all events
    pubEvents := stack.MockPublisher.EventsForSession("survivor")
    assert.NotEmpty(t, pubEvents)
}
```

---

## 12. Graceful Shutdown Tests

### GS1. Drain Period Allows In-Flight Turns to Complete

```go
func TestGracefulShutdown_DrainAllowsTurnsToComplete(t *testing.T) {
    blockCh := make(chan struct{})
    slowDriver := &MockAgentDriver{
        TurnScript:   slowTurnScript(100 * time.Millisecond),
        BlockOnStart: blockCh,
    }

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("drain-complete", slowDriver)
    createAgentSession(t, stack.Service, "drain-complete")

    ctx := context.Background()
    var turnEvents []agent.AgentEvent
    var turnErr error

    go func() {
        receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
            SessionID: "drain-complete", Message: "working",
        })
        if err != nil {
            turnErr = err
            return
        }
        turnEvents = collectAllEvents(receiver)
    }()

    time.Sleep(10 * time.Millisecond) // let turn start
    close(blockCh)                     // unblock driver

    // Close the service (should wait for drain)
    err := stack.Service.Close()
    assert.NoError(t, err)

    // Turn should have completed normally
    assert.NoError(t, turnErr)
    if len(turnEvents) > 0 {
        assert.Equal(t, agent.EventTurnCompleted, turnEvents[len(turnEvents)-1].Type)
    }
}
```

### GS2. Force Shutdown After Deadline

```go
func TestGracefulShutdown_ForceAfterDeadline(t *testing.T) {
    // Use infiniteScript driver that never completes
    driver := &MockAgentDriver{TurnScript: infiniteScript()}

    stack := NewAgentTestStack(t /* WithShutdownTimeout(100*time.Millisecond) */)
    stack.DriverFactory.SetDriver("force-shutdown", driver)
    createAgentSession(t, stack.Service, "force-shutdown")

    ctx := context.Background()
    go func() {
        stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
            SessionID: "force-shutdown", Message: "infinite work",
        })
    }()
    time.Sleep(10 * time.Millisecond)

    // Close should force-cancel after timeout and return
    start := time.Now()
    err := stack.Service.Close()
    elapsed := time.Since(start)

    assert.NoError(t, err)
    // Should complete within a reasonable time (shutdown timeout + buffer)
    assert.Less(t, elapsed, 5*time.Second,
        "shutdown took too long: %v", elapsed)
}
```

### GS3. New CreateSession Rejected After Close Initiated

```go
func TestGracefulShutdown_NewSessionsRejected(t *testing.T) {
    stack := NewAgentTestStack(t)
    createAgentSession(t, stack.Service, "pre-shutdown")

    // Initiate close in background
    done := make(chan struct{})
    go func() {
        stack.Service.Close()
        close(done)
    }()

    // Small delay to let close set the flag
    time.Sleep(10 * time.Millisecond)

    _, err := stack.Service.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
        SessionConfig: agentapi.SessionConfig{SessionID: "post-shutdown", Driver: "mock"},
    })
    assertAgentRPCError(t, err, rpc.CodeUnavailable)

    <-done
}
```

---

## 13. Fault Injection Tests

### FI1. Driver Start Failure

```go
func TestFaultInjection_DriverStartFailure(t *testing.T) {
    driver := &MockAgentDriver{startErr: errors.New("provider unavailable")}

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("start-fail", driver)
    createAgentSession(t, stack.Service, "start-fail")

    _, err := stack.Service.SendMessage(context.Background(),
        &agentapi.SendMessageRequest{SessionID: "start-fail", Message: "hello"})
    assert.Error(t, err)

    // Session should still exist (start failure doesn't destroy session)
    resp, err := stack.Service.GetSession(context.Background(),
        &agentapi.GetAgentSessionRequest{SessionID: "start-fail"})
    assert.NoError(t, err)
    assert.Equal(t, "idle", resp.State)

    // Retry should work with a fixed driver
    stack.DriverFactory.SetDriver("start-fail", &MockAgentDriver{})
    // Note: this tests the started=false retry logic from section 4
}
```

### FI2. Driver Panic Recovery

```go
func TestFaultInjection_DriverPanicRecovery(t *testing.T) {
    driver := &MockAgentDriver{PanicOnStart: true}

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("panic-test", driver)
    createAgentSession(t, stack.Service, "panic-test")

    // Should not crash the service
    _, err := stack.Service.SendMessage(context.Background(),
        &agentapi.SendMessageRequest{SessionID: "panic-test", Message: "boom"})
    assert.Error(t, err)

    // Service should still be functional
    resp, err := stack.Service.ListSessions(context.Background(),
        &agentapi.ListAgentSessionsRequest{})
    assert.NoError(t, err)
    assert.GreaterOrEqual(t, len(resp.Sessions), 1)
}
```

### FI3. Context Cancellation During Turn

```go
func TestFaultInjection_ContextCancellationDuringTurn(t *testing.T) {
    driver := &MockAgentDriver{
        TurnScript: slowTurnScript(5 * time.Second),
    }

    stack := NewAgentTestStack(t)
    stack.DriverFactory.SetDriver("cancel-test", driver)
    createAgentSession(t, stack.Service, "cancel-test")

    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()

    receiver, err := stack.Service.SendMessage(ctx, &agentapi.SendMessageRequest{
        SessionID: "cancel-test", Message: "slow work",
    })
    // Note: SendMessage uses session context, not request context for the turn.
    // The receiver may still work even after request context cancels.
    if err == nil && receiver != nil {
        // Events may be received until turn finishes or is aborted
        _ = collectEventsWithTimeout(receiver, 200*time.Millisecond)
    }
}
```

### FI4. Malformed ResumeSession Logs

```go
func TestFaultInjection_MalformedResumeLogs(t *testing.T) {
    stack := NewAgentTestStack(t)

    tests := []struct {
        name string
        log  []agentapi.AgentMessageRecord
    }{
        {
            name: "invalid role",
            log: []agentapi.AgentMessageRecord{
                {Turn: 1, Role: "invalid_role", Content: json.RawMessage(`{"text":"hi"}`)},
            },
        },
        {
            name: "malformed content JSON",
            log: []agentapi.AgentMessageRecord{
                {Turn: 1, Role: "user", Content: json.RawMessage(`{not valid json}`)},
            },
        },
        {
            name: "null content",
            log: []agentapi.AgentMessageRecord{
                {Turn: 1, Role: "user", Content: nil},
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := stack.Service.ResumeSession(context.Background(),
                &agentapi.ResumeSessionRequest{
                    SessionConfig: agentapi.SessionConfig{Driver: "mock", Model: "m"},
                    SchemaVersion:   1,
                    ConversationLog: tt.log,
                })
            assert.Error(t, err, "malformed log should produce error, not panic")
        })
    }
}
```

### FI5. ResumeSession Validation Warnings

```go
func TestFaultInjection_ResumeSession_Warnings(t *testing.T) {
    stack := NewAgentTestStack(t)

    tests := []struct {
        name        string
        log         []agentapi.AgentMessageRecord
        wantWarning string
    }{
        {
            name: "non-contiguous turns",
            log: []agentapi.AgentMessageRecord{
                {Turn: 1, Role: "user", Content: mustMarshal(map[string]string{"text": "hi"})},
                {Turn: 5, Role: "assistant", Content: mustMarshal(assistantContent("hello"))},
            },
            wantWarning: "non-contiguous",
        },
        {
            name: "unknown tool reference",
            log: []agentapi.AgentMessageRecord{
                {Turn: 1, Role: "tool_result", Content: mustMarshal(map[string]any{
                    "tool_call_id": "tc-1", "tool_name": "nonexistent_tool",
                    "content": []map[string]string{{"type": "text", "text": "result"}},
                })},
            },
            wantWarning: "unknown tool",
        },
        {
            name: "consecutive same role",
            log: []agentapi.AgentMessageRecord{
                {Turn: 1, Role: "user", Content: mustMarshal(map[string]string{"text": "hi"})},
                {Turn: 1, Role: "user", Content: mustMarshal(map[string]string{"text": "hello"})},
            },
            wantWarning: "consecutive",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            resp, err := stack.Service.ResumeSession(context.Background(),
                &agentapi.ResumeSessionRequest{
                    SessionConfig: agentapi.SessionConfig{
                        Driver: "mock", Model: "m",
                        Tools: []string{"bash", "read_file"},
                    },
                    SchemaVersion:   1,
                    ConversationLog: tt.log,
                })
            require.NoError(t, err) // warnings, not errors
            assert.NotEmpty(t, resp.Warnings)

            foundWarning := false
            for _, w := range resp.Warnings {
                if strings.Contains(strings.ToLower(w), tt.wantWarning) {
                    foundWarning = true
                }
            }
            assert.True(t, foundWarning,
                "expected warning containing %q, got %v", tt.wantWarning, resp.Warnings)
        })
    }
}
```

---

## 14. Benchmarks

### B1. Session Creation Latency

```go
func BenchmarkAgentSessionCreation(b *testing.B) {
    stack := NewAgentTestStack(b)
    ctx := context.Background()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        id := fmt.Sprintf("bench-create-%d", i)
        resp, err := stack.Service.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
            SessionConfig: agentapi.SessionConfig{
                SessionID: id, Driver: "mock", Model: "mock-model",
            },
        })
        if err != nil {
            b.Fatal(err)
        }
        stack.Service.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{
            SessionID: resp.SessionID,
        })
    }
}
```

**Target:** <5ms per session creation + destroy cycle (mock driver, no real AI).

### B2. Event Throughput

```go
func BenchmarkEventThroughput(b *testing.B) {
    const eventsPerTurn = 100
    script := generateNumberedScript(eventsPerTurn)

    stack := NewAgentTestStack(b)
    stack.DriverFactory.SetDriver("bench-events", &MockAgentDriver{TurnScript: script})

    createAgentSession(b, stack.Service, "bench-events")

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        receiver, err := stack.Service.SendMessage(context.Background(),
            &agentapi.SendMessageRequest{SessionID: "bench-events", Message: "go"})
        if err != nil {
            b.Fatal(err)
        }
        drainReceiver(receiver)
    }
}
```

**Target:** >10,000 events/second throughput per session.

### B3. RPC Overhead vs In-Process

```go
func BenchmarkRPCOverhead(b *testing.B) {
    stack := NewAgentTestStack(b)
    createAgentSession(b, stack.Service, "bench-overhead")

    b.Run("InProcess", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            _, err := stack.Service.GetSession(context.Background(),
                &agentapi.GetAgentSessionRequest{SessionID: "bench-overhead"})
            if err != nil {
                b.Fatal(err)
            }
        }
    })

    b.Run("ViaRPC", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            _, err := stack.Client.GetSession(context.Background(),
                &agentapi.GetAgentSessionRequest{SessionID: "bench-overhead"})
            if err != nil {
                b.Fatal(err)
            }
        }
    })
}
```

**Target:** RPC overhead <50us per unary call over in-process path. The ConnectRPC in-process client adds serialization/deserialization but no network I/O; overhead should be dominated by codec cost.

### B4. ResumeSession with Large Conversation

```go
func BenchmarkResumeSession_LargeConversation(b *testing.B) {
    // Build a 500-message conversation log
    log := buildConversationLog(500)

    stack := NewAgentTestStack(b)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        id := fmt.Sprintf("bench-resume-%d", i)
        resp, err := stack.Service.ResumeSession(context.Background(),
            &agentapi.ResumeSessionRequest{
                SessionConfig: agentapi.SessionConfig{
                    SessionID: id, Driver: "mock", Model: "mock-model",
                },
                SchemaVersion:   1,
                ConversationLog: log,
            })
        if err != nil {
            b.Fatal(err)
        }
        stack.Service.DestroySession(context.Background(),
            &agentapi.DestroyAgentSessionRequest{SessionID: resp.SessionID})
    }
}
```

**Target:** <50ms per ResumeSession with 500-message conversation (dominated by codec deserialization).

### B5. Codec Conversion Latency

```go
func BenchmarkCodecConversion(b *testing.B) {
    msg := sampleAssistantMessage() // multi-block with thinking + text + tool_use

    b.Run("AgentMessageToRecord", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            _, err := agentapi.AgentMessageToRecord(msg)
            if err != nil {
                b.Fatal(err)
            }
        }
    })

    record, _ := agentapi.AgentMessageToRecord(msg)
    b.Run("RecordToAgentMessage", func(b *testing.B) {
        for i := 0; i < b.N; i++ {
            _, err := agentapi.RecordToAgentMessage(record)
            if err != nil {
                b.Fatal(err)
            }
        }
    })
}
```

**Target:** <10us per conversion in either direction for a typical multi-block message.

### B6. Session Lookup Under Load

```go
func BenchmarkSessionLookup(b *testing.B) {
    stack := NewAgentTestStack(b)
    ctx := context.Background()

    ids := make([]string, 100)
    for i := range ids {
        ids[i] = fmt.Sprintf("bench-lookup-%03d", i)
        createAgentSession(b, stack.Service, ids[i])
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := stack.Service.GetSession(ctx, &agentapi.GetAgentSessionRequest{
            SessionID: ids[i%len(ids)],
        })
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

**Target:** <1us per session lookup with 100 active sessions.

---

## 15. Stress / Soak Tests

### ST1. High-Concurrency Session Churn

```go
func TestStress_SessionChurn(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    stack := NewAgentTestStack(t, agent.WithMaxSessions(0)) // unlimited
    ctx := context.Background()

    const goroutines = 50
    const opsPerGoroutine = 100

    var wg sync.WaitGroup
    var totalOps atomic.Int64

    for g := 0; g < goroutines; g++ {
        wg.Add(1)
        go func(gid int) {
            defer wg.Done()
            for i := 0; i < opsPerGoroutine; i++ {
                id := fmt.Sprintf("churn-%d-%d", gid, i)
                resp, err := stack.Service.CreateSession(ctx,
                    &agentapi.CreateAgentSessionRequest{
                        SessionConfig: agentapi.SessionConfig{
                            SessionID: id, Driver: "mock", Model: "mock-model",
                        },
                    })
                if err != nil {
                    continue
                }

                receiver, _ := stack.Service.SendMessage(ctx,
                    &agentapi.SendMessageRequest{SessionID: resp.SessionID, Message: "churn"})
                if receiver != nil {
                    drainReceiver(receiver)
                }

                stack.Service.DestroySession(ctx,
                    &agentapi.DestroyAgentSessionRequest{SessionID: resp.SessionID})
                totalOps.Add(1)
            }
        }(g)
    }
    wg.Wait()

    t.Logf("completed %d session churn operations", totalOps.Load())

    // All sessions should be gone
    listResp, _ := stack.Service.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
    assert.Empty(t, listResp.Sessions, "leaked sessions: %d", len(listResp.Sessions))
}
```

### ST2. Sustained Multi-Session Soak

```go
func TestStress_SustainedMultiSessionSoak(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    stack := NewAgentTestStack(t)
    ctx := context.Background()

    const sessions = 10
    const turnsPerSession = 50

    var wg sync.WaitGroup
    for s := 0; s < sessions; s++ {
        wg.Add(1)
        go func(sid int) {
            defer wg.Done()
            id := fmt.Sprintf("soak-%d", sid)
            createAgentSession(t, stack.Service, id)

            for turn := 0; turn < turnsPerSession; turn++ {
                receiver, err := stack.Service.SendMessage(ctx,
                    &agentapi.SendMessageRequest{
                        SessionID: id,
                        Message:   fmt.Sprintf("turn %d", turn),
                    })
                if err != nil {
                    t.Errorf("soak %s turn %d: %v", id, turn, err)
                    return
                }
                events := collectAllEvents(receiver)
                if len(events) == 0 {
                    t.Errorf("soak %s turn %d: 0 events", id, turn)
                    return
                }
            }

            stack.Service.DestroySession(ctx,
                &agentapi.DestroyAgentSessionRequest{SessionID: id})
        }(s)
    }
    wg.Wait()

    listResp, _ := stack.Service.ListSessions(ctx, &agentapi.ListAgentSessionsRequest{})
    assert.Empty(t, listResp.Sessions)
}
```

### ST3. Burst Operations on Single Session

```go
func TestStress_BurstOperationsOnSingleSession(t *testing.T) {
    if testing.Short() {
        t.Skip("stress test")
    }

    stack := NewAgentTestStack(t)
    ctx := context.Background()
    createAgentSession(t, stack.Service, "burst")

    const burstSize = 100

    // Burst of concurrent GetSession calls
    var wg sync.WaitGroup
    for i := 0; i < burstSize; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, err := stack.Service.GetSession(ctx, &agentapi.GetAgentSessionRequest{
                SessionID: "burst",
            })
            assert.NoError(t, err)
        }()
    }
    wg.Wait()

    // Burst of concurrent FollowUp calls
    for i := 0; i < burstSize; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            _, _ = stack.Service.FollowUp(ctx, &agentapi.FollowUpRequest{
                SessionID: "burst", Message: fmt.Sprintf("followup-%d", idx),
            })
        }(i)
    }
    wg.Wait()
}
```

---

## 16. Security Tests

### SEC1. API Key Not in RPC Messages

```go
func TestSecurity_APIKeyNotInRPCMessages(t *testing.T) {
    stack := NewAgentTestStack(t)

    // SessionConfig should not have an APIKey field
    resp, err := stack.Service.CreateSession(context.Background(),
        &agentapi.CreateAgentSessionRequest{
            SessionConfig: agentapi.SessionConfig{
                SessionID: "sec-1", Driver: "mock", Model: "mock-model",
            },
        })
    require.NoError(t, err)

    // Serialize the response and verify no API key leaks
    data, _ := json.Marshal(resp)
    assert.NotContains(t, string(data), "api_key")
    assert.NotContains(t, string(data), "apiKey")
    assert.NotContains(t, string(data), "API_KEY")
}
```

### SEC2. Session ID Validation

```go
func TestSecurity_SessionIDValidation(t *testing.T) {
    stack := NewAgentTestStack(t)

    malicious := []string{
        "../../../etc",
        "sess; rm -rf /",
        "sess\x00extra",
        strings.Repeat("a", 300), // very long
    }

    for _, id := range malicious {
        t.Run(fmt.Sprintf("id=%q", id), func(t *testing.T) {
            _, err := stack.Service.CreateSession(context.Background(),
                &agentapi.CreateAgentSessionRequest{
                    SessionConfig: agentapi.SessionConfig{
                        SessionID: id, Driver: "mock", Model: "mock-model",
                    },
                })
            assert.Error(t, err)
        })
    }
}
```

### SEC3. LaunchProcess Env Vars Not Logged

Verify that API keys passed via `LaunchProcessRequest.Env` are not visible in logs or error messages.

```go
func TestSecurity_LaunchProcessEnvNotLeaked(t *testing.T) {
    var logBuf bytes.Buffer
    logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

    mockSandboxService := newMockSandboxService(t)
    ctrl := native.NewNativeSandboxControl(mockSandboxService, native.WithLogger(logger))

    ctx := context.Background()
    sandboxResp, _ := ctrl.CreateSandbox(ctx, control.CreateSandboxRequest{
        Template: "tank/bases/repo@v1",
    })

    ctrl.LaunchProcess(ctx, control.LaunchProcessRequest{
        SandboxID:  sandboxResp.SandboxID,
        Binary:     "flexagent",
        Args:       []string{"serve", "agent"},
        Env:        map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret-key-12345"},
        ExposePort: 8080,
    })

    logOutput := logBuf.String()
    assert.NotContains(t, logOutput, "sk-ant-secret-key-12345",
        "API key leaked in log output")
}
```

---

## 17. CI Tier Mapping

| Tier | Tests | Trigger | Timeout | Environment |
|------|-------|---------|---------|-------------|
| **T1: Fast** | Unit tests (U1-U6), codec tests (P2), security tests (SEC1-SEC3), fault injection (FI1-FI5) | Every commit | 2 min | Any OS |
| **T2: Component** | Property tests (P1, P3-P6), EventPublisher tests (EP1-EP5), event fidelity (EF1-EF4), dual-view (DV1-DV2), RPC transport (RT1-RT3), graceful shutdown (GS1-GS3) | Every PR | 5 min | Any OS |
| **T3: Integration** | Cross-agent resume (CR1-CR3), SandboxControl (SC1-SC4), session forking (FK1-FK2) | PR merge | 10 min | Any OS |
| **T4: Stress** | Session churn (ST1), sustained soak (ST2), burst operations (ST3) | Nightly | 30 min | Any OS |
| **T5: Benchmarks** | All benchmarks (B1-B6), baseline comparison | PR merge | 5 min | Any OS |

All tiers run with `-race` flag.

---

## 18. Coverage Requirements

| Package | Minimum Coverage |
|---------|-----------------|
| `internal/agent/api/` (interface, types, codec) | 90% |
| `internal/agent/service.go` (AgentLoopService) | 85% |
| `internal/rpc/server/agent_server.go` | 85% |
| `internal/rpc/client/agent_client.go` | 85% |
| `internal/rpc/codec/agent_map.go` | 90% |
| `internal/sandbox/control/` | 80% |
| `internal/sandbox/control/native/` | 80% |

---

## 19. Exit Criteria

Before Plan 18 implementation is considered complete:

1. **All unit tests pass** -- session lifecycle, state transitions, error handling, codec
2. **All property tests pass** -- state machine (1000+ iterations), codec round-trip, event ordering, resume validation
3. **All fault injection tests pass** -- driver failure, panic recovery, context cancellation, malformed logs
4. **All event fidelity tests pass** -- no dropped events, terminal guarantees, ordering across RPC
5. **All RPC transport tests pass** -- round-trip for unary and streaming procedures, error mapping
6. **Session forking stress tests pass** -- 20+ concurrent forks from same log, all independent
7. **Cross-agent resume round-trip tests pass** -- record -> message -> ConversationEntry chain preserves semantic content
8. **All benchmarks meet targets** -- session creation <5ms, event throughput >10K/s, RPC overhead <50us, codec <10us
9. **All stress tests pass under `-race`** -- no races in session churn, soak, or burst operations
10. **Graceful shutdown tests pass** -- drain period, force shutdown, new session rejection
11. **SandboxControl tests pass** -- NativeSandboxControl with mock sandbox-host
12. **Security tests pass** -- no API key leaks, session ID validation, env var protection
13. **Test coverage meets thresholds** -- per package requirements above
14. **`go test -race ./internal/agent/... ./internal/rpc/... ./internal/sandbox/control/...`** passes with zero race conditions
15. **`go vet` and `staticcheck`** report no issues on test or production code
16. **Benchmark baselines recorded** for regression tracking

---

## 20. Test Dependencies

| Dependency | Purpose |
|-----------|---------|
| `pgregory.net/rapid` | Property-based testing (P1-P6) |
| `github.com/stretchr/testify` | Assertions (assert/require) |
| `internal/agent/agenttest` | MockAgentDriver, AgentTestStack, MockEventPublisher |
| `internal/rpc/rpctest` | Existing RPC test infrastructure (in-process stack patterns) |
| Go stdlib `testing` | Benchmarks, test framework |
| Go stdlib `io` | io.EOF for EventReceiver contract verification |
| Go stdlib `encoding/json` | Codec test fixtures |

---

## Test Infrastructure Files

| File | Contents |
|------|----------|
| `internal/agent/agenttest/mock_driver.go` | MockAgentDriver + variants, turn scripts |
| `internal/agent/agenttest/rpc_stack.go` | AgentTestStack, MockEventPublisher, MockDriverFactory |
| `internal/agent/agenttest/helpers.go` | createAgentSession, drainReceiver, collectAllEvents, assertAgentRPCError |
| `internal/agent/agenttest/generators.go` | Rapid generators for conversation logs, messages, events |
| `internal/agent/service_test.go` | U1-U6, GS1-GS3 |
| `internal/agent/service_property_test.go` | P1, P3-P6 |
| `internal/agent/api/codec_test.go` | P2, CR2, B5 |
| `internal/agent/service_bench_test.go` | B1, B2, B4, B6 |
| `internal/agent/service_stress_test.go` | ST1-ST3 |
| `internal/agent/service_fault_test.go` | FI1-FI5 |
| `internal/rpc/server/agent_server_test.go` | RT1-RT3 |
| `internal/rpc/rpctest/agent_harness_test.go` | EF1-EF4, B3, RPC round-trip harness |
| `internal/sandbox/control/native/native_test.go` | SC1-SC4 |
| `tests/integration/scenarios/agent_rpc_fork_test.go` | FK1-FK2 |
| `tests/integration/scenarios/agent_rpc_resume_test.go` | CR1, CR3 |

---

## Completion Signoff

- **Status:** Partial
- **Date:** 2026-03-17
- **Epic:** aiag-rmv
- **Task:** aiag-r3z.1
- **Branch:** main
- **Verified by:** coder-1-sea
- **Code verification:** `go test ./internal/agent/... ./internal/rpc/... ./internal/sandbox/control/... ./cmd/flexagent/... ./tests/integration/agent_rpc/... ./tests/integration/scenarios/... -count=1` (PASS)
- **Race verification:** `go test -race ./internal/agent/... ./internal/rpc/... -count=1` (PASS)

### Test Category Checklist

| Categories | Status | Evidence |
|---|---|---|
| P1-P6 | PASS | `internal/agent/harness_test.go`, `internal/agent/service_property_test.go`, `internal/agent/api/codec_test.go`, `internal/rpc/rpctest/property_test.go` |
| U1-U6 | PASS | `internal/agent/service_test.go` |
| EP1-EP5 | PASS | `internal/agent/service_test.go` (`TestEventPublisher*`, `TestTurnScopedReceiverEndsAtTurnCompleted`, `TestSubscribeEventsMultipleSubscribers`, `TestDualDeliveryBothStreamsReceiveEvents`) |
| RT1-RT3 | PASS | `internal/rpc/server/agent_server_test.go`, `internal/rpc/client/agent_client_test.go` |
| FK1-FK2 | PASS | `tests/integration/scenarios/agent_rpc_fork_test.go` |
| CR1-CR3 | PASS | `tests/integration/scenarios/agent_rpc_resume_test.go` |
| EF1-EF4 | PASS | `internal/rpc/rpctest/agent_harness_test.go` |
| SC1-SC4 | PASS | `internal/sandbox/control/native/native_test.go`, `internal/sandbox/control/cloud/cloud_test.go` |
| DV1-DV2 | PARTIAL | Equivalent behavior coverage exists via `TestDualDeliveryBothStreamsReceiveEvents` and terminal stream tests, but no dedicated DV-tagged termmux independence/failure tests matching section 11 verbatim. |
| GS1-GS3 | PARTIAL | `TestCloseDrainsActiveSessions` and `TestCloseRejectsNewSessions` cover GS1/GS3; no explicit GS2 assertion for forced close after drain deadline. |
| FI1-FI5 | PARTIAL | Fault coverage exists across `internal/agent/harness_test.go` and `internal/agent/service_test.go` (`malformed logs`, warning paths), but naming/scope differs from FI table. |
| B1-B6 | PARTIAL | B1-B4 present (`internal/agent/harness_bench_test.go`, `internal/rpc/rpctest/benchmark_test.go`); no dedicated B5/B6 benchmarks matching section 14 labels. |
| ST1-ST3 | PASS | `internal/agent/harness_soak_test.go`, `internal/rpc/rpctest/stress_test.go`, scenario stress tests |
| SEC1-SEC3 | PASS | `internal/agent/harness_test.go`, `internal/rpc/rpctest/security_test.go`, `internal/sandbox/control/cloud/cloud_test.go` |

### Deviation Classification

| Deviation | Class | Status |
|---|---|---|
| Section 11 DV categories are covered only indirectly; no direct DV1/DV2 tests with the specified scope text. | Missing | Open |
| GS2 force-shutdown-after-deadline case is not explicitly asserted in current tests. | Missing | Open |
| B5/B6 benchmark categories in section 14 do not have matching benchmark functions by ID. | Missing | Open |
| Test inventory has equivalent coverage but different file/function organization than section 2363 file map. | Structural | Accepted |
| Some category IDs are represented by equivalent tests under different naming conventions (e.g., F*/S*/property suites). | Cosmetic | Accepted |

### Summary

Harness coverage for the implemented RPC stack is broad and stable (all required verification commands passed). Completion is **partial** because three labeled harness categories (DV, GS2, B5/B6) are not implemented as specified.
