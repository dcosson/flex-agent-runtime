package ai

import (
	"sort"
	"time"
)

// ToolCallIDNormalizer maps tool-call IDs when crossing provider/model boundaries.
type ToolCallIDNormalizer func(id string, model Model, source *AssistantMessage) string

// TransformMessages transforms a conversation for replay on a (possibly different) model.
func TransformMessages(messages []Message, targetModel Model, normalizeToolCallID ToolCallIDNormalizer) []Message {
	toolCallIDMap := make(map[string]string)
	transformed := make([]Message, 0, len(messages))

	for _, msg := range messages {
		switch m := msg.(type) {
		case *UserMessage:
			transformed = append(transformed, m)
		case *AssistantMessage:
			t := transformAssistantMessage(m, targetModel, normalizeToolCallID, toolCallIDMap)
			if t != nil {
				transformed = append(transformed, t)
			}
		case *ToolResultMessage:
			transformed = append(transformed, transformToolResult(m, toolCallIDMap))
		}
	}

	return insertSyntheticToolResults(transformed)
}

func transformAssistantMessage(msg *AssistantMessage, targetModel Model, normalizeID ToolCallIDNormalizer, idMap map[string]string) *AssistantMessage {
	if msg.StopReason == StopReasonError || msg.StopReason == StopReasonAborted {
		return nil
	}

	sameModel := msg.Provider == targetModel.Provider && msg.API == targetModel.API && msg.Model == targetModel.ID
	newContent := make([]ContentBlock, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *TextContent:
			if sameModel {
				newContent = append(newContent, b)
			} else {
				newContent = append(newContent, &TextContent{Text: b.Text})
			}
		case *ThinkingContent:
			if sameModel {
				newContent = append(newContent, b)
			} else if b.Redacted {
				continue
			} else if b.Thinking != "" {
				newContent = append(newContent, &TextContent{Text: b.Thinking})
			}
		case *ToolCall:
			tc := &ToolCall{ID: b.ID, Name: b.Name, Arguments: b.Arguments}
			if sameModel {
				tc.ThoughtSignature = b.ThoughtSignature
			}
			if normalizeID != nil && !sameModel {
				newID := normalizeID(b.ID, targetModel, msg)
				if newID != b.ID {
					idMap[b.ID] = newID
					tc.ID = newID
				}
			}
			newContent = append(newContent, tc)
		}
	}
	return &AssistantMessage{
		Content:    newContent,
		API:        msg.API,
		Provider:   msg.Provider,
		Model:      msg.Model,
		Usage:      msg.Usage,
		StopReason: msg.StopReason,
		Timestamp:  msg.Timestamp,
	}
}

func transformToolResult(msg *ToolResultMessage, idMap map[string]string) *ToolResultMessage {
	newID := msg.ToolCallID
	if mapped, ok := idMap[msg.ToolCallID]; ok {
		newID = mapped
	}
	if newID == msg.ToolCallID {
		return msg
	}
	return &ToolResultMessage{
		ToolCallID: newID,
		ToolName:   msg.ToolName,
		Content:    msg.Content,
		IsError:    msg.IsError,
		Timestamp:  msg.Timestamp,
	}
}

func insertSyntheticToolResults(messages []Message) []Message {
	result := make([]Message, 0, len(messages))
	pendingToolCalls := make(map[string]*ToolCall)
	lastTimestamp := int64(0)

	flush := func(ts int64) {
		if len(pendingToolCalls) == 0 {
			return
		}
		ids := make([]string, 0, len(pendingToolCalls))
		for id := range pendingToolCalls {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			tc := pendingToolCalls[id]
			result = append(result, &ToolResultMessage{
				ToolCallID: id,
				ToolName:   tc.Name,
				Content:    []ContentBlock{&TextContent{Text: "No result provided"}},
				IsError:    true,
				Timestamp:  ts,
			})
		}
		pendingToolCalls = make(map[string]*ToolCall)
	}

	for _, msg := range messages {
		switch m := msg.(type) {
		case *AssistantMessage:
			flush(m.GetTimestamp())
			for _, block := range m.Content {
				if tc, ok := block.(*ToolCall); ok {
					pendingToolCalls[tc.ID] = tc
				}
			}
			lastTimestamp = m.GetTimestamp()
			result = append(result, m)
		case *ToolResultMessage:
			delete(pendingToolCalls, m.ToolCallID)
			lastTimestamp = m.GetTimestamp()
			result = append(result, m)
		default:
			lastTimestamp = m.GetTimestamp()
			result = append(result, m)
		}
	}
	if lastTimestamp == 0 {
		lastTimestamp = TimeToMillis(time.Now())
	}
	flush(lastTimestamp)
	return result
}
