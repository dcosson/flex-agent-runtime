package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/ai"
)

// NativeDriver implements the canonical LLM -> tools -> LLM turn loop.
type NativeDriver struct {
	mu           sync.Mutex
	cfg          DriverConfig
	agent        *Agent
	bus          *eventBus
	cancel       context.CancelFunc
	running      bool
	pendingSteer string
	followUps    []string
	turn         int
}

func NewNativeDriver(cfg DriverConfig) *NativeDriver {
	return &NativeDriver{cfg: cfg, bus: newEventBus()}
}

func (d *NativeDriver) bindAgent(a *Agent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.agent = a
}

func (d *NativeDriver) Subscribe(fn func(AgentEvent)) func() {
	_, unsub := d.bus.subscribe(fn)
	return unsub
}

func (d *NativeDriver) Start(ctx context.Context, session *Session, prompt string) error {
	return d.startLoop(ctx, session, prompt)
}

func (d *NativeDriver) Resume(ctx context.Context, session *Session) error {
	return d.startLoop(ctx, session, "")
}

func (d *NativeDriver) startLoop(ctx context.Context, session *Session, prompt string) error {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return BusyError{State: StateStreaming}
	}
	if d.agent == nil {
		d.mu.Unlock()
		return fmt.Errorf("native driver is not bound to agent")
	}
	if session == nil {
		d.mu.Unlock()
		return fmt.Errorf("session is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.running = true
	d.mu.Unlock()

	go d.run(runCtx, session.Clone(), prompt)
	return nil
}

func (d *NativeDriver) Stop(ctx context.Context) error {
	_ = ctx
	d.mu.Lock()
	cancel := d.cancel
	d.cancel = nil
	d.running = false
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (d *NativeDriver) run(ctx context.Context, session *Session, initialPrompt string) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("native driver panic: %v\n%s", r, debug.Stack())
			d.agent.RecordError()
			d.emit(AgentEvent{Type: EventDriverError, Error: err, ErrorMessage: err.Error()})
			_ = d.agent.Transition(StateIdle)
		}
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		d.agent.onDriverIdle()
	}()

	nextPrompt := initialPrompt
	for {
		if ctx.Err() != nil {
			d.emit(AgentEvent{Type: EventAborted, ErrorMessage: ctx.Err().Error()})
			_ = d.agent.Transition(StateIdle)
			return
		}

		if nextPrompt != "" {
			d.appendUserMessage(nextPrompt)
		}

		if err := d.executeSingleTurn(ctx); err != nil {
			d.agent.RecordError()
			d.emit(AgentEvent{Type: EventDriverError, Error: err, ErrorMessage: err.Error()})
			_ = d.agent.Transition(StateIdle)
			return
		}

		var done bool
		nextPrompt, done = d.nextPromptFromControlQueue()
		if done {
			_ = d.agent.Transition(StateIdle)
			return
		}
	}
}

func (d *NativeDriver) executeSingleTurn(ctx context.Context) error {
	d.turn++
	d.agent.MarkTurnStarted()
	d.emit(AgentEvent{Type: EventTurnStarted, Turn: d.turn})

	terminal := false
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := d.agent.Transition(StateStreaming); err != nil {
			return err
		}

		asst, err := d.callProvider(ctx)
		if err != nil {
			d.emit(AgentEvent{Type: EventProviderError, Turn: d.turn, Error: err, ErrorMessage: err.Error()})
			return err
		}

		assistantMsg := AgentMessage{Turn: d.turn, Message: asst, CreatedAt: time.Now()}
		if err := d.agent.AppendConversation(assistantMsg); err != nil {
			return err
		}
		d.emit(AgentEvent{Type: EventAgentMessageCompleted, Turn: d.turn, Message: &assistantMsg, Assistant: asst})

		toolCalls := extractToolCalls(asst)
		if len(toolCalls) == 0 {
			break
		}

		if err := d.agent.Transition(StateToolExecution); err != nil {
			return err
		}
		for _, tc := range toolCalls {
			if err := d.executeTool(ctx, tc); err != nil {
				return err
			}
			if d.isTerminalTool(tc.Name) {
				terminal = true
				break
			}
		}
		if terminal {
			break
		}
	}

	d.agent.MarkTurnCompleted()
	d.emit(AgentEvent{Type: EventTurnCompleted, Turn: d.turn})
	return nil
}

func (d *NativeDriver) callProvider(ctx context.Context) (*ai.AssistantMessage, error) {
	sess := d.agent.Session()
	if sess == nil {
		return nil, fmt.Errorf("session missing")
	}
	llmMsgs := make([]ai.Message, 0, len(sess.ConversationLog))
	for _, m := range sess.ConversationLog {
		llmMsgs = append(llmMsgs, m.Message)
	}
	llmCtx := ai.Context{
		SystemPrompt: d.cfg.SystemPrompt,
		Messages:     ai.TransformMessages(llmMsgs, d.cfg.Model, nil),
		Tools:        d.toolSchemas(),
	}

	es := ai.StreamSimple(ctx, d.cfg.Model, llmCtx, d.cfg.Options)
	for ev := range es.C {
		switch ev.Type {
		case ai.EventTextDelta:
			d.emit(AgentEvent{Type: EventAgentMessageDelta, Turn: d.turn, Delta: ev.Delta})
		case ai.EventThinkingDelta:
			d.emit(AgentEvent{Type: EventThinkingDelta, Turn: d.turn, Delta: ev.Delta})
		}
	}

	msg, err := es.Result()
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

func (d *NativeDriver) executeTool(ctx context.Context, tc *ai.ToolCall) error {
	tool, ok := d.lookupTool(tc.Name)
	if !ok {
		res := AgentToolResult{IsError: true, Content: []ai.ContentBlock{&ai.TextContent{Text: "tool not found: " + tc.Name}}}
		return d.appendToolResult(tc, tc.Name, res, fmt.Errorf("tool not found: %s", tc.Name))
	}

	d.agent.RecordToolStarted()
	d.emit(AgentEvent{Type: EventToolStarted, Turn: d.turn, ToolName: tc.Name, ToolCallID: tc.ID})
	result, err := tool.Execute(ctx, tc.ID, tc.Arguments, func(update AgentToolResult) {
		upd := update.clone()
		d.emit(AgentEvent{Type: EventToolUpdate, Turn: d.turn, ToolName: tc.Name, ToolCallID: tc.ID, ToolResult: &upd})
	})

	if err != nil {
		result.IsError = true
		if len(result.Content) == 0 {
			result.Content = []ai.ContentBlock{&ai.TextContent{Text: err.Error()}}
		}
	}

	d.agent.RecordToolFinished()
	if err := d.appendToolResult(tc, tc.Name, result, err); err != nil {
		return err
	}

	if tool.Terminal {
		resCopy := result.clone()
		d.emit(AgentEvent{Type: EventTerminalToolCompleted, Turn: d.turn, ToolName: tc.Name, ToolCallID: tc.ID, ToolResult: &resCopy})
	}
	return nil
}

func (d *NativeDriver) appendToolResult(tc *ai.ToolCall, toolName string, result AgentToolResult, execErr error) error {
	resultMsg := &ai.ToolResultMessage{
		ToolCallID: tc.ID,
		ToolName:   toolName,
		Content:    result.Content,
		IsError:    result.IsError,
		Timestamp:  ai.TimeToMillis(time.Now()),
	}
	entry := AgentMessage{Turn: d.turn, Message: resultMsg, CreatedAt: time.Now()}
	if err := d.agent.AppendConversation(entry); err != nil {
		return err
	}

	resCopy := result.clone()
	d.emit(AgentEvent{Type: EventToolCompleted, Turn: d.turn, ToolName: toolName, ToolCallID: tc.ID, ToolResult: &resCopy})
	if execErr != nil {
		d.emit(AgentEvent{Type: EventToolError, Turn: d.turn, ToolName: toolName, ToolCallID: tc.ID, Error: execErr, ErrorMessage: execErr.Error()})
	}
	return nil
}

func (d *NativeDriver) nextPromptFromControlQueue() (string, bool) {
	cmds := d.agent.ControlQueue().Drain()
	if len(cmds) == 0 && len(d.followUps) == 0 && d.pendingSteer == "" {
		return "", true
	}

	for _, cmd := range cmds {
		switch cmd.Type {
		case ControlAbort:
			d.emit(AgentEvent{Type: EventAborted, ControlMessage: cmd.Message})
			return "", true
		case ControlSteer:
			d.pendingSteer = cmd.Message
		case ControlFollowUp:
			d.followUps = append(d.followUps, cmd.Message)
		}
	}

	if d.pendingSteer != "" {
		prompt := d.pendingSteer
		d.pendingSteer = ""
		return prompt, false
	}
	if len(d.followUps) > 0 {
		prompt := d.followUps[0]
		d.followUps = d.followUps[1:]
		return prompt, false
	}
	return "", true
}

func (d *NativeDriver) appendUserMessage(prompt string) {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return
	}
	_ = d.agent.AppendConversation(AgentMessage{
		Turn:      d.turn + 1,
		CreatedAt: time.Now(),
		Message: &ai.UserMessage{
			Content:   []ai.ContentBlock{&ai.TextContent{Text: trimmed}},
			Timestamp: ai.TimeToMillis(time.Now()),
		},
	})
}

func (d *NativeDriver) emit(event AgentEvent) {
	d.bus.publish(event)
}

func (d *NativeDriver) lookupTool(name string) (AgentTool, bool) {
	for _, t := range d.cfg.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return AgentTool{}, false
}

func (d *NativeDriver) isTerminalTool(name string) bool {
	tool, ok := d.lookupTool(name)
	return ok && tool.Terminal
}

func (d *NativeDriver) toolSchemas() []ai.Tool {
	out := make([]ai.Tool, 0, len(d.cfg.Tools))
	for _, t := range d.cfg.Tools {
		out = append(out, t.Tool)
	}
	return out
}

func extractToolCalls(msg *ai.AssistantMessage) []*ai.ToolCall {
	if msg == nil {
		return nil
	}
	out := make([]*ai.ToolCall, 0)
	for _, block := range msg.Content {
		if tc, ok := block.(*ai.ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}
