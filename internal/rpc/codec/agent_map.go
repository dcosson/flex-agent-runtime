package codec

import (
	"github.com/dcosson/flex-agent-runtime/internal/agent"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
)

// CopySessionMetrics preserves the wire-shape while keeping mapping explicit.
func CopySessionMetrics(in agentapi.SessionMetrics) agentapi.SessionMetrics {
	return agentapi.SessionMetrics{
		TurnsStarted:      in.TurnsStarted,
		TurnsCompleted:    in.TurnsCompleted,
		MessagesAppended:  in.MessagesAppended,
		ToolCallsStarted:  in.ToolCallsStarted,
		ToolCallsFinished: in.ToolCallsFinished,
		Errors:            in.Errors,
	}
}

// ToRuntimeEvent converts the agent API event to the legacy runtime event type
// used by the existing RPC event envelope.
func ToRuntimeEvent(evt agentapi.AgentEvent) agent.AgentEvent {
	out := agent.AgentEvent{
		Type:            agent.AgentEventType(evt.Type),
		SessionID:       evt.SessionID,
		DriverSessionID: evt.DriverSessionID,
		State:           agent.AgentState(evt.State),
		Turn:            evt.Turn,
		Assistant:       copyAssistantMessage(evt.Assistant),
		ToolName:        evt.ToolName,
		ToolCallID:      evt.ToolCallID,
		Delta:           evt.Delta,
		ControlMessage:  evt.ControlMessage,
		ErrorMessage:    evt.ErrorMessage,
		At:              evt.At,
		Metadata:        copyMap(evt.Metadata),
	}
	if evt.Message != nil {
		out.Message = &agent.AgentMessage{
			Turn:      evt.Message.Turn,
			Message:   evt.Message.Message,
			CreatedAt: evt.Message.CreatedAt,
		}
	}
	if evt.ToolResult != nil {
		out.ToolResult = &agent.AgentToolResult{
			Content:    copyContentBlocks(evt.ToolResult.Content),
			SnapshotID: evt.ToolResult.SnapshotID,
			ExitCode:   copyIntPtr(evt.ToolResult.ExitCode),
			IsError:    evt.ToolResult.IsError,
			Metadata:   copyMap(evt.ToolResult.Metadata),
		}
	}
	return out
}

// FromRuntimeEvent converts the legacy runtime event type to the agent API type.
func FromRuntimeEvent(evt agent.AgentEvent) agentapi.AgentEvent {
	out := agentapi.AgentEvent{
		Type:            agentapi.AgentEventType(evt.Type),
		SessionID:       evt.SessionID,
		DriverSessionID: evt.DriverSessionID,
		State:           agentapi.AgentState(evt.State),
		Turn:            evt.Turn,
		Assistant:       copyAssistantMessage(evt.Assistant),
		ToolName:        evt.ToolName,
		ToolCallID:      evt.ToolCallID,
		Delta:           evt.Delta,
		ControlMessage:  evt.ControlMessage,
		ErrorMessage:    evt.ErrorMessage,
		At:              evt.At,
		Metadata:        copyMap(evt.Metadata),
	}
	if evt.Message != nil {
		out.Message = &agentapi.AgentMessage{
			Turn:      evt.Message.Turn,
			Message:   evt.Message.Message,
			CreatedAt: evt.Message.CreatedAt,
		}
	}
	if evt.ToolResult != nil {
		out.ToolResult = &agentapi.AgentToolResult{
			Content:    copyContentBlocks(evt.ToolResult.Content),
			SnapshotID: evt.ToolResult.SnapshotID,
			ExitCode:   copyIntPtr(evt.ToolResult.ExitCode),
			IsError:    evt.ToolResult.IsError,
			Metadata:   copyMap(evt.ToolResult.Metadata),
		}
	}
	return out
}

// WrapEventReceiver adapts agentapi.EventReceiver to rpc/api.AgentEventReceiver.
func WrapEventReceiver(sessionID string, recv agentapi.EventReceiver) api.AgentEventReceiver {
	if recv == nil {
		return nil
	}
	return &eventEnvelopeReceiver{sessionID: sessionID, recv: recv}
}

// UnwrapEventReceiver adapts rpc/api.AgentEventReceiver to agentapi.EventReceiver.
func UnwrapEventReceiver(recv api.AgentEventReceiver) agentapi.EventReceiver {
	if recv == nil {
		return nil
	}
	return &agentEventReceiver{recv: recv}
}

type eventEnvelopeReceiver struct {
	sessionID string
	recv      agentapi.EventReceiver
}

func (r *eventEnvelopeReceiver) Recv() (*api.AgentEventEnvelope, error) {
	evt, err := r.recv.Recv()
	if err != nil {
		return nil, err
	}
	if evt == nil {
		return nil, nil
	}
	out := ToRuntimeEvent(*evt)
	id := r.sessionID
	if id == "" {
		id = evt.SessionID
	}
	if out.SessionID == "" {
		out.SessionID = id
	}
	return &api.AgentEventEnvelope{SessionID: id, Event: out}, nil
}

func (r *eventEnvelopeReceiver) Close() error {
	return r.recv.Close()
}

type agentEventReceiver struct {
	recv api.AgentEventReceiver
}

func (r *agentEventReceiver) Recv() (*agentapi.AgentEvent, error) {
	env, err := r.recv.Recv()
	if err != nil {
		return nil, err
	}
	if env == nil {
		return nil, nil
	}
	evt := FromRuntimeEvent(env.Event)
	if evt.SessionID == "" {
		evt.SessionID = env.SessionID
	}
	return &evt, nil
}

func (r *agentEventReceiver) Close() error {
	return r.recv.Close()
}

func copyIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}

func copyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyContentBlocks(blocks []ai.ContentBlock) []ai.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	return append([]ai.ContentBlock(nil), blocks...)
}

func copyAssistantMessage(msg *ai.AssistantMessage) *ai.AssistantMessage {
	if msg == nil {
		return nil
	}
	cp := *msg
	if len(msg.Content) > 0 {
		cp.Content = append([]ai.ContentBlock(nil), msg.Content...)
	}
	return &cp
}
