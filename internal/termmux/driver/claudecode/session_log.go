package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/termmux/driver"
)

// ParseSessionLog reads Claude Code's session.jsonl format and returns
// canonical conversation entries.
func ParseSessionLog(reader io.Reader) ([]driver.ConversationEntry, error) {
	var entries []driver.ConversationEntry
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024) // 1MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var raw claudeSessionLine
		if err := json.Unmarshal(line, &raw); err != nil {
			// Skip malformed lines
			continue
		}

		entry := convertClaudeToCanonical(raw)
		entries = append(entries, entry...)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan session log: %w", err)
	}

	return entries, nil
}

// WriteSessionLog writes canonical conversation entries back into
// Claude Code's session.jsonl format.
func WriteSessionLog(entries []driver.ConversationEntry, w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	for _, entry := range entries {
		line := convertCanonicalToClaude(entry)
		if err := enc.Encode(line); err != nil {
			return fmt.Errorf("encode session log line: %w", err)
		}
	}

	return nil
}

// Claude Code session.jsonl line format
type claudeSessionLine struct {
	Type      string               `json:"type,omitempty"`
	Role      string               `json:"role"`
	Content   json.RawMessage      `json:"content"` // Can be string or array of content blocks
	Timestamp string               `json:"timestamp,omitempty"`
	Usage     *claudeUsage         `json:"usage,omitempty"`
	Thinking  []claudeThinkingItem `json:"thinking,omitempty"`
}

type claudeUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	CacheRead    int64 `json:"cache_read_input_tokens,omitempty"`
}

type claudeThinkingItem struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

// claudeContentBlock represents a content block in Claude Code's format
type claudeContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Content json.RawMessage `json:"content,omitempty"` // For tool_result
}

// convertClaudeToCanonical converts a Claude session log line to canonical entries.
// One Claude line may produce multiple canonical entries (e.g., if it has both
// text and tool_use blocks).
func convertClaudeToCanonical(raw claudeSessionLine) []driver.ConversationEntry {
	ts := parseTimestamp(raw.Timestamp)
	role := driver.ConversationRole(raw.Role)

	// Try parsing content as a string first
	var textContent string
	if err := json.Unmarshal(raw.Content, &textContent); err == nil {
		entry := driver.ConversationEntry{
			Timestamp: ts,
			Role:      role,
			Content:   textContent,
		}
		if raw.Usage != nil {
			entry.Usage = &driver.TokenUsage{
				InputTokens:  raw.Usage.InputTokens,
				OutputTokens: raw.Usage.OutputTokens,
				CachedTokens: raw.Usage.CacheRead,
			}
		}
		if len(raw.Thinking) > 0 {
			var thinking string
			for _, t := range raw.Thinking {
				if t.Content != "" {
					if thinking != "" {
						thinking += "\n"
					}
					thinking += t.Content
				}
			}
			if thinking != "" {
				entry.Thinking = &driver.ThinkingBlock{Content: thinking}
			}
		}
		return []driver.ConversationEntry{entry}
	}

	// Try parsing as array of content blocks
	var blocks []claudeContentBlock
	if err := json.Unmarshal(raw.Content, &blocks); err != nil {
		return nil
	}

	var entries []driver.ConversationEntry
	for _, block := range blocks {
		switch block.Type {
		case "text":
			entry := driver.ConversationEntry{
				Timestamp: ts,
				Role:      role,
				Content:   block.Text,
			}
			if raw.Usage != nil {
				entry.Usage = &driver.TokenUsage{
					InputTokens:  raw.Usage.InputTokens,
					OutputTokens: raw.Usage.OutputTokens,
					CachedTokens: raw.Usage.CacheRead,
				}
			}
			entries = append(entries, entry)

		case "tool_use":
			entries = append(entries, driver.ConversationEntry{
				Timestamp: ts,
				Role:      driver.RoleToolUse,
				ToolCall: &driver.ToolCallRecord{
					Name:   block.Name,
					Args:   block.Input,
					CallID: block.ID,
				},
			})

		case "tool_result":
			var resultText string
			if err := json.Unmarshal(block.Content, &resultText); err != nil {
				resultText = string(block.Content)
			}
			entries = append(entries, driver.ConversationEntry{
				Timestamp: ts,
				Role:      driver.RoleToolResult,
				Content:   resultText,
				ToolCall: &driver.ToolCallRecord{
					CallID: block.ID,
					Result: resultText,
				},
			})
		}
	}

	// Add thinking blocks
	if len(raw.Thinking) > 0 && len(entries) > 0 {
		var thinking string
		for _, t := range raw.Thinking {
			if t.Content != "" {
				if thinking != "" {
					thinking += "\n"
				}
				thinking += t.Content
			}
		}
		if thinking != "" {
			entries[0].Thinking = &driver.ThinkingBlock{Content: thinking}
		}
	}

	return entries
}

// convertCanonicalToClaude converts a canonical entry back to a Claude session log line.
func convertCanonicalToClaude(entry driver.ConversationEntry) claudeSessionLine {
	line := claudeSessionLine{
		Timestamp: entry.Timestamp.Format(time.RFC3339Nano),
	}

	switch entry.Role {
	case driver.RoleUser:
		line.Role = "user"
		content, _ := json.Marshal(entry.Content)
		line.Content = content

	case driver.RoleAssistant:
		line.Role = "assistant"
		if entry.ToolCall == nil {
			content, _ := json.Marshal(entry.Content)
			line.Content = content
		} else {
			// Emit as content block array with tool_use
			blocks := []claudeContentBlock{}
			if entry.Content != "" {
				blocks = append(blocks, claudeContentBlock{
					Type: "text",
					Text: entry.Content,
				})
			}
			blocks = append(blocks, claudeContentBlock{
				Type:  "tool_use",
				ID:    entry.ToolCall.CallID,
				Name:  entry.ToolCall.Name,
				Input: entry.ToolCall.Args,
			})
			content, _ := json.Marshal(blocks)
			line.Content = content
		}

	case driver.RoleToolUse:
		line.Role = "assistant"
		blocks := []claudeContentBlock{{
			Type:  "tool_use",
			ID:    entry.ToolCall.CallID,
			Name:  entry.ToolCall.Name,
			Input: entry.ToolCall.Args,
		}}
		content, _ := json.Marshal(blocks)
		line.Content = content

	case driver.RoleToolResult:
		line.Role = "user"
		result := entry.Content
		if entry.ToolCall != nil && entry.ToolCall.Result != "" {
			result = entry.ToolCall.Result
		}
		resultJSON, _ := json.Marshal(result)
		blocks := []claudeContentBlock{{
			Type:    "tool_result",
			ID:      entry.ToolCall.CallID,
			Content: resultJSON,
		}}
		content, _ := json.Marshal(blocks)
		line.Content = content
	}

	if entry.Usage != nil {
		line.Usage = &claudeUsage{
			InputTokens:  entry.Usage.InputTokens,
			OutputTokens: entry.Usage.OutputTokens,
			CacheRead:    entry.Usage.CachedTokens,
		}
	}

	if entry.Thinking != nil {
		line.Thinking = []claudeThinkingItem{{
			Type:    "thinking",
			Content: entry.Thinking.Content,
		}}
	}

	return line
}

func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Now()
	}
	return t
}
