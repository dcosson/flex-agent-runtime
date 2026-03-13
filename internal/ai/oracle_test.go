package ai

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// =============================================================================
// Wire format types for JSON serialization to/from TypeScript
// =============================================================================

// wireMessage is a flat JSON representation matching the TS Message type.
// The "role" field discriminates between user/assistant/toolResult.
type wireMessage struct {
	Role         string        `json:"role"`
	Content      []wireContent `json:"content"`
	Timestamp    int64         `json:"timestamp"`
	API          string        `json:"api,omitempty"`
	Provider     string        `json:"provider,omitempty"`
	Model        string        `json:"model,omitempty"`
	Usage        *wireUsage    `json:"usage,omitempty"`
	StopReason   string        `json:"stopReason,omitempty"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
	ToolCallID   string        `json:"toolCallId,omitempty"`
	ToolName     string        `json:"toolName,omitempty"`
	IsError      *bool         `json:"isError,omitempty"`
}

type wireContent struct {
	Type              string         `json:"type"`
	Text              string         `json:"text,omitempty"`
	TextSignature     string         `json:"textSignature,omitempty"`
	Thinking          string         `json:"thinking,omitempty"`
	ThinkingSignature string         `json:"thinkingSignature,omitempty"`
	ID                string         `json:"id,omitempty"`
	Name              string         `json:"name,omitempty"`
	Arguments         map[string]any `json:"arguments,omitempty"`
	ThoughtSignature  string         `json:"thoughtSignature,omitempty"`
	Data              string         `json:"data,omitempty"`
	MimeType          string         `json:"mimeType,omitempty"`
}

type wireUsage struct {
	Input       int          `json:"input"`
	Output      int          `json:"output"`
	CacheRead   int          `json:"cacheRead"`
	CacheWrite  int          `json:"cacheWrite"`
	TotalTokens int          `json:"totalTokens"`
	Cost        wireUsageCost `json:"cost"`
}

type wireUsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

type wireModel struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	API           string        `json:"api"`
	Provider      string        `json:"provider"`
	BaseURL       string        `json:"baseUrl"`
	Reasoning     bool          `json:"reasoning"`
	Input         []string      `json:"input"`
	Cost          wireModelCost `json:"cost"`
	ContextWindow int           `json:"contextWindow"`
	MaxTokens     int           `json:"maxTokens"`
}

type wireModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// =============================================================================
// Go ↔ Wire conversion
// =============================================================================

func goMsgToWire(msg Message) wireMessage {
	switch m := msg.(type) {
	case *UserMessage:
		return wireMessage{
			Role:      "user",
			Content:   goContentBlocksToWire(m.Content),
			Timestamp: m.Timestamp,
		}
	case *AssistantMessage:
		wm := wireMessage{
			Role:       "assistant",
			Content:    goContentBlocksToWire(m.Content),
			Timestamp:  m.Timestamp,
			API:        m.API,
			Provider:   m.Provider,
			Model:      m.Model,
			StopReason: string(m.StopReason),
			Usage: &wireUsage{
				Input:       m.Usage.Input,
				Output:      m.Usage.Output,
				CacheRead:   m.Usage.CacheRead,
				CacheWrite:  m.Usage.CacheWrite,
				TotalTokens: m.Usage.TotalTokens,
				Cost: wireUsageCost{
					Input:     m.Usage.Cost.Input,
					Output:    m.Usage.Cost.Output,
					CacheRead: m.Usage.Cost.CacheRead,
					CacheWrite: m.Usage.Cost.CacheWrite,
					Total:     m.Usage.Cost.Total,
				},
			},
		}
		if m.ErrorMessage != "" {
			wm.ErrorMessage = m.ErrorMessage
		}
		return wm
	case *ToolResultMessage:
		isErr := m.IsError
		return wireMessage{
			Role:       "toolResult",
			Content:    goContentBlocksToWire(m.Content),
			Timestamp:  m.Timestamp,
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
			IsError:    &isErr,
		}
	}
	return wireMessage{}
}

func goContentBlocksToWire(blocks []ContentBlock) []wireContent {
	result := make([]wireContent, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, goContentToWire(b))
	}
	return result
}

func goContentToWire(block ContentBlock) wireContent {
	switch b := block.(type) {
	case *TextContent:
		wc := wireContent{Type: "text", Text: b.Text}
		if b.TextSignature != "" {
			wc.TextSignature = b.TextSignature
		}
		return wc
	case *ThinkingContent:
		wc := wireContent{Type: "thinking", Thinking: b.Thinking}
		if b.ThinkingSignature != "" {
			wc.ThinkingSignature = b.ThinkingSignature
		}
		return wc
	case *ToolCall:
		wc := wireContent{
			Type:      "toolCall",
			ID:        b.ID,
			Name:      b.Name,
			Arguments: b.Arguments,
		}
		if b.ThoughtSignature != "" {
			wc.ThoughtSignature = b.ThoughtSignature
		}
		return wc
	case *ImageContent:
		return wireContent{Type: "image", Data: b.Data, MimeType: b.MimeType}
	}
	return wireContent{}
}

func wireMsgToGo(wm wireMessage) Message {
	switch wm.Role {
	case "user":
		return &UserMessage{
			Content:   wireContentBlocksToGo(wm.Content),
			Timestamp: wm.Timestamp,
		}
	case "assistant":
		m := &AssistantMessage{
			Content:      wireContentBlocksToGo(wm.Content),
			Timestamp:    wm.Timestamp,
			API:          wm.API,
			Provider:     wm.Provider,
			Model:        wm.Model,
			StopReason:   StopReason(wm.StopReason),
			ErrorMessage: wm.ErrorMessage,
		}
		if wm.Usage != nil {
			m.Usage = Usage{
				Input:       wm.Usage.Input,
				Output:      wm.Usage.Output,
				CacheRead:   wm.Usage.CacheRead,
				CacheWrite:  wm.Usage.CacheWrite,
				TotalTokens: wm.Usage.TotalTokens,
				Cost: UsageCost{
					Input:      wm.Usage.Cost.Input,
					Output:     wm.Usage.Cost.Output,
					CacheRead:  wm.Usage.Cost.CacheRead,
					CacheWrite: wm.Usage.Cost.CacheWrite,
					Total:      wm.Usage.Cost.Total,
				},
			}
		}
		return m
	case "toolResult":
		isErr := false
		if wm.IsError != nil {
			isErr = *wm.IsError
		}
		return &ToolResultMessage{
			ToolCallID: wm.ToolCallID,
			ToolName:   wm.ToolName,
			Content:    wireContentBlocksToGo(wm.Content),
			IsError:    isErr,
			Timestamp:  wm.Timestamp,
		}
	}
	return nil
}

func wireContentBlocksToGo(blocks []wireContent) []ContentBlock {
	result := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		result = append(result, wireContentToGo(b))
	}
	return result
}

func wireContentToGo(wc wireContent) ContentBlock {
	switch wc.Type {
	case "text":
		return &TextContent{Text: wc.Text, TextSignature: wc.TextSignature}
	case "thinking":
		return &ThinkingContent{Thinking: wc.Thinking, ThinkingSignature: wc.ThinkingSignature}
	case "toolCall":
		return &ToolCall{ID: wc.ID, Name: wc.Name, Arguments: wc.Arguments, ThoughtSignature: wc.ThoughtSignature}
	case "image":
		return &ImageContent{Data: wc.Data, MimeType: wc.MimeType}
	}
	return nil
}

func goMsgsToWire(msgs []Message) []wireMessage {
	result := make([]wireMessage, len(msgs))
	for i, m := range msgs {
		result[i] = goMsgToWire(m)
	}
	return result
}

func wireModelFromGo(m Model) wireModel {
	return wireModel{
		ID:            m.ID,
		Name:          m.Name,
		API:           m.API,
		Provider:      m.Provider,
		BaseURL:       m.BaseURL,
		Reasoning:     m.Reasoning,
		Input:         m.Input,
		Cost:          wireModelCost{Input: m.Cost.Input, Output: m.Cost.Output, CacheRead: m.Cost.CacheRead, CacheWrite: m.Cost.CacheWrite},
		ContextWindow: m.ContextWindow,
		MaxTokens:     m.MaxTokens,
	}
}

// =============================================================================
// TypeScript harness subprocess
// =============================================================================

type tsHarness struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

func newTSHarness(t *testing.T) *tsHarness {
	t.Helper()

	// Find the harness relative to this test file.
	_, thisFile, _, _ := runtime.Caller(0)
	harnessPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "oracle", "harness.mjs")
	nodeModules := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "oracle", "node_modules")

	if _, err := os.Stat(nodeModules); os.IsNotExist(err) {
		t.Skip("oracle harness not installed: run 'npm install' in testdata/oracle/")
	}

	cmd := exec.Command("node", harnessPath)
	cmd.Dir = filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "oracle")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start harness: %v", err)
	}

	t.Cleanup(func() {
		stdin.Close()
		cmd.Wait()
	})

	return &tsHarness{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
	}
}

func (h *tsHarness) call(req any) (json.RawMessage, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	if _, err := h.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write to harness: %w", err)
	}
	if !h.stdout.Scan() {
		if err := h.stdout.Err(); err != nil {
			return nil, fmt.Errorf("read from harness: %w", err)
		}
		return nil, fmt.Errorf("harness closed stdout unexpectedly")
	}

	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(h.stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w (raw: %s)", err, h.stdout.Text())
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("TS error: %s", resp.Error)
	}
	return resp.Result, nil
}

func (h *tsHarness) transformMessages(t *testing.T, msgs []wireMessage, model wireModel) []wireMessage {
	t.Helper()
	result, err := h.call(map[string]any{
		"cmd":      "transformMessages",
		"messages": msgs,
		"model":    model,
	})
	if err != nil {
		t.Fatalf("transformMessages: %v", err)
	}

	var out []wireMessage
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal transformMessages result: %v", err)
	}
	return out
}

func (h *tsHarness) calculateCost(t *testing.T, model wireModel, usage wireUsage) wireUsageCost {
	t.Helper()
	result, err := h.call(map[string]any{
		"cmd":   "calculateCost",
		"model": model,
		"usage": usage,
	})
	if err != nil {
		t.Fatalf("calculateCost: %v", err)
	}

	var cost wireUsageCost
	if err := json.Unmarshal(result, &cost); err != nil {
		t.Fatalf("unmarshal calculateCost result: %v", err)
	}
	return cost
}

func (h *tsHarness) isContextOverflow(t *testing.T, msg wireMessage, contextWindow int) bool {
	t.Helper()
	result, err := h.call(map[string]any{
		"cmd":           "isContextOverflow",
		"message":       msg,
		"contextWindow": contextWindow,
	})
	if err != nil {
		t.Fatalf("isContextOverflow: %v", err)
	}

	var overflow bool
	if err := json.Unmarshal(result, &overflow); err != nil {
		t.Fatalf("unmarshal isContextOverflow result: %v", err)
	}
	return overflow
}

// =============================================================================
// Test corpus
// =============================================================================

type transformTestCase struct {
	Name        string        `json:"name"`
	Messages    []wireMessage `json:"messages"`
	TargetModel wireModel     `json:"targetModel"`
}

type costTestCase struct {
	Name  string    `json:"name"`
	Model wireModel `json:"model"`
	Usage wireUsage `json:"usage"`
}

type overflowTestCase struct {
	Name          string      `json:"name"`
	Message       wireMessage `json:"message"`
	ContextWindow int         `json:"contextWindow"`
}

// buildTransformCorpus generates 100+ diverse transform test cases.
func buildTransformCorpus() []transformTestCase {
	anthropicModel := wireModel{
		ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4",
		API: "anthropic-messages", Provider: "anthropic",
		BaseURL: "https://api.anthropic.com", Reasoning: false,
		Input: []string{"text", "image"},
		Cost:  wireModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75},
		ContextWindow: 200000, MaxTokens: 8192,
	}
	openaiModel := wireModel{
		ID: "gpt-4o", Name: "GPT-4o",
		API: "openai-completions", Provider: "openai",
		BaseURL: "https://api.openai.com/v1", Reasoning: false,
		Input: []string{"text", "image"},
		Cost:  wireModelCost{Input: 2.5, Output: 10, CacheRead: 1.25, CacheWrite: 2.5},
		ContextWindow: 128000, MaxTokens: 16384,
	}

	boolTrue := true
	boolFalse := false
	ts := int64(1710000000000) // fixed timestamp for determinism

	var corpus []transformTestCase

	// --- Simple text conversations ---
	for i := 0; i < 10; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("simple_text_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: fmt.Sprintf("Hello %d", i)}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: fmt.Sprintf("Hi there %d!", i)}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 10, Output: 5, TotalTokens: 15},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Single tool call with result ---
	for i := 0; i < 10; i++ {
		tcID := fmt.Sprintf("tc_%d", i)
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("single_tool_call_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "search for cats"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "text", Text: "Let me search."},
					{Type: "toolCall", ID: tcID, Name: "search", Arguments: map[string]any{"query": fmt.Sprintf("cats%d", i)}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 20, Output: 15, TotalTokens: 35},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: tcID, ToolName: "search",
					Content: []wireContent{{Type: "text", Text: "Found 42 results"}},
					IsError: &boolFalse, Timestamp: ts + 2000,
				},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "I found 42 results about cats."}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 50, Output: 20, TotalTokens: 70},
					Timestamp: ts + 3000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Multiple tool calls in one turn ---
	for i := 0; i < 10; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("multi_tool_call_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "read and write files"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "toolCall", ID: fmt.Sprintf("read_%d", i), Name: "readFile", Arguments: map[string]any{"path": "/tmp/a"}},
					{Type: "toolCall", ID: fmt.Sprintf("write_%d", i), Name: "writeFile", Arguments: map[string]any{"path": "/tmp/b", "content": "hello"}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 30, Output: 25, TotalTokens: 55},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("read_%d", i), ToolName: "readFile",
					Content: []wireContent{{Type: "text", Text: "file contents"}},
					IsError: &boolFalse, Timestamp: ts + 2000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("write_%d", i), ToolName: "writeFile",
					Content: []wireContent{{Type: "text", Text: "ok"}},
					IsError: &boolFalse, Timestamp: ts + 2001,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Cross-model switch (Anthropic → OpenAI) ---
	for i := 0; i < 10; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("cross_model_anthropic_to_openai_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "explain recursion"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "text", Text: "Recursion is when a function calls itself.", TextSignature: "sig-abc"},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 10, Output: 15, TotalTokens: 25},
					Timestamp: ts + 1000,
				},
				{Role: "user", Content: []wireContent{{Type: "text", Text: "give an example"}}, Timestamp: ts + 2000},
			},
			TargetModel: openaiModel,
		})
	}

	// --- Cross-model switch (OpenAI → Anthropic) ---
	for i := 0; i < 10; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("cross_model_openai_to_anthropic_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "explain monads"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "text", Text: "A monad is a design pattern.", TextSignature: "sig-xyz"},
				},
					API: "openai-completions", Provider: "openai", Model: "gpt-4o",
					StopReason: "stop", Usage: &wireUsage{Input: 10, Output: 15, TotalTokens: 25},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Thinking blocks (same model — keep) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("thinking_same_model_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "think about this"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "thinking", Thinking: fmt.Sprintf("Let me consider option %d...", i), ThinkingSignature: fmt.Sprintf("tsig-%d", i)},
					{Type: "text", Text: fmt.Sprintf("After thinking, I believe %d.", i)},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 10, Output: 30, TotalTokens: 40},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Thinking blocks (cross-model — convert to text) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("thinking_cross_model_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "analyze this"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "thinking", Thinking: fmt.Sprintf("Deep analysis %d...", i)},
					{Type: "text", Text: fmt.Sprintf("My analysis %d", i)},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 10, Output: 30, TotalTokens: 40},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: openaiModel,
		})
	}

	// --- Empty thinking blocks (should be dropped in cross-model) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("empty_thinking_cross_model_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "quick question"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "thinking", Thinking: ""},
					{Type: "text", Text: fmt.Sprintf("The answer is %d", i)},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 10, Output: 10, TotalTokens: 20},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: openaiModel,
		})
	}

	// --- Error/aborted messages (should be dropped) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("error_message_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "do something"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "partial response"}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "error", ErrorMessage: "context overflow",
					Usage: &wireUsage{Input: 100, Output: 5, TotalTokens: 105},
					Timestamp: ts + 1000,
				},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: fmt.Sprintf("retry response %d", i)}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 50, Output: 20, TotalTokens: 70},
					Timestamp: ts + 2000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Aborted messages ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("aborted_message_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "long task"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "starting..."}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "aborted",
					Usage: &wireUsage{Input: 10, Output: 5, TotalTokens: 15},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Orphaned tool calls (no results provided) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("orphaned_tool_calls_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "run tools"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "toolCall", ID: fmt.Sprintf("orphan_a_%d", i), Name: "toolA", Arguments: map[string]any{"x": i}},
					{Type: "toolCall", ID: fmt.Sprintf("orphan_b_%d", i), Name: "toolB", Arguments: map[string]any{"y": i}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 20, Output: 20, TotalTokens: 40},
					Timestamp: ts + 1000,
				},
				// No tool results — next assistant should trigger synthetic results
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "continuing without results"}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 60, Output: 15, TotalTokens: 75},
					Timestamp: ts + 3000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Partially orphaned tool calls (some results, some missing) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("partial_orphan_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "run two tools"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "toolCall", ID: fmt.Sprintf("has_result_%d", i), Name: "search", Arguments: map[string]any{"q": "cats"}},
					{Type: "toolCall", ID: fmt.Sprintf("no_result_%d", i), Name: "browse", Arguments: map[string]any{"url": "example.com"}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 25, Output: 20, TotalTokens: 45},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("has_result_%d", i), ToolName: "search",
					Content: []wireContent{{Type: "text", Text: "found results"}},
					IsError: &boolFalse, Timestamp: ts + 2000,
				},
				// No result for browse — orphaned
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "done"}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 70, Output: 10, TotalTokens: 80},
					Timestamp: ts + 3000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Tool call with error result ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("tool_error_result_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "read file"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "toolCall", ID: fmt.Sprintf("err_tc_%d", i), Name: "readFile", Arguments: map[string]any{"path": "/nonexistent"}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 15, Output: 10, TotalTokens: 25},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("err_tc_%d", i), ToolName: "readFile",
					Content: []wireContent{{Type: "text", Text: "file not found"}},
					IsError: &boolTrue, Timestamp: ts + 2000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Tool calls with thoughtSignature (same model — keep, cross model — strip) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("tool_thought_signature_same_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "search"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "toolCall", ID: fmt.Sprintf("tsig_%d", i), Name: "search",
						Arguments: map[string]any{"q": "test"}, ThoughtSignature: fmt.Sprintf("thought-sig-%d", i)},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 10, Output: 10, TotalTokens: 20},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("tsig_%d", i), ToolName: "search",
					Content: []wireContent{{Type: "text", Text: "results"}},
					IsError: &boolFalse, Timestamp: ts + 2000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Tool calls with thoughtSignature (cross model — strip signature) ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("tool_thought_signature_cross_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "search cross"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "toolCall", ID: fmt.Sprintf("tsig_cross_%d", i), Name: "search",
						Arguments: map[string]any{"q": "test"}, ThoughtSignature: fmt.Sprintf("thought-sig-%d", i)},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 10, Output: 10, TotalTokens: 20},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("tsig_cross_%d", i), ToolName: "search",
					Content: []wireContent{{Type: "text", Text: "results"}},
					IsError: &boolFalse, Timestamp: ts + 2000,
				},
			},
			TargetModel: openaiModel,
		})
	}

	// --- Multi-turn mixed conversation ---
	for i := 0; i < 5; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("multi_turn_mixed_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "start project"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{
					{Type: "thinking", Thinking: "Let me plan this...", ThinkingSignature: "sig-plan"},
					{Type: "text", Text: "I'll help you set up the project."},
					{Type: "toolCall", ID: fmt.Sprintf("init_%d", i), Name: "createDir", Arguments: map[string]any{"path": "/tmp/proj"}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 20, Output: 40, TotalTokens: 60},
					Timestamp: ts + 1000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("init_%d", i), ToolName: "createDir",
					Content: []wireContent{{Type: "text", Text: "created"}},
					IsError: &boolFalse, Timestamp: ts + 2000,
				},
				{Role: "assistant", Content: []wireContent{
					{Type: "text", Text: "Directory created. Now let me add files."},
					{Type: "toolCall", ID: fmt.Sprintf("write_%d", i), Name: "writeFile",
						Arguments: map[string]any{"path": "/tmp/proj/main.go", "content": "package main"}},
				},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "toolUse", Usage: &wireUsage{Input: 70, Output: 30, TotalTokens: 100},
					Timestamp: ts + 3000,
				},
				{Role: "toolResult", ToolCallID: fmt.Sprintf("write_%d", i), ToolName: "writeFile",
					Content: []wireContent{{Type: "text", Text: "written"}},
					IsError: &boolFalse, Timestamp: ts + 4000,
				},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "Project initialized!"}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "stop", Usage: &wireUsage{Input: 110, Output: 10, TotalTokens: 120},
					Timestamp: ts + 5000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Length stop reason ---
	for i := 0; i < 3; i++ {
		corpus = append(corpus, transformTestCase{
			Name: fmt.Sprintf("length_stop_%d", i),
			Messages: []wireMessage{
				{Role: "user", Content: []wireContent{{Type: "text", Text: "write a long essay"}}, Timestamp: ts},
				{Role: "assistant", Content: []wireContent{{Type: "text", Text: "Here is a very long essay..."}},
					API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
					StopReason: "length", Usage: &wireUsage{Input: 10, Output: 8192, TotalTokens: 8202},
					Timestamp: ts + 1000,
				},
			},
			TargetModel: anthropicModel,
		})
	}

	// --- Empty content blocks ---
	corpus = append(corpus, transformTestCase{
		Name: "empty_assistant_content",
		Messages: []wireMessage{
			{Role: "user", Content: []wireContent{{Type: "text", Text: "hello"}}, Timestamp: ts},
			{Role: "assistant", Content: []wireContent{},
				API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
				StopReason: "stop", Usage: &wireUsage{Input: 5, Output: 0, TotalTokens: 5},
				Timestamp: ts + 1000,
			},
		},
		TargetModel: anthropicModel,
	})

	// --- Text signature stripping on cross-model ---
	corpus = append(corpus, transformTestCase{
		Name: "text_signature_cross_model",
		Messages: []wireMessage{
			{Role: "user", Content: []wireContent{{Type: "text", Text: "test"}}, Timestamp: ts},
			{Role: "assistant", Content: []wireContent{
				{Type: "text", Text: "response with signature", TextSignature: "text-sig-123"},
			},
				API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-20250514",
				StopReason: "stop", Usage: &wireUsage{Input: 5, Output: 10, TotalTokens: 15},
				Timestamp: ts + 1000,
			},
		},
		TargetModel: openaiModel,
	})

	return corpus
}

// buildCostCorpus generates diverse cost calculation test cases.
func buildCostCorpus() []costTestCase {
	var corpus []costTestCase

	models := []wireModel{
		{ID: "claude-sonnet-4-20250514", Provider: "anthropic", Cost: wireModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}},
		{ID: "gpt-4o", Provider: "openai", Cost: wireModelCost{Input: 2.5, Output: 10, CacheRead: 1.25, CacheWrite: 2.5}},
		{ID: "gemini-2.5-pro", Provider: "google", Cost: wireModelCost{Input: 1.25, Output: 10, CacheRead: 0.3125, CacheWrite: 1.25}},
	}

	usages := []struct {
		name  string
		usage wireUsage
	}{
		{"zero", wireUsage{Cost: wireUsageCost{}}},
		{"small", wireUsage{Input: 100, Output: 50, CacheRead: 0, CacheWrite: 0, TotalTokens: 150, Cost: wireUsageCost{}}},
		{"medium", wireUsage{Input: 5000, Output: 2000, CacheRead: 1000, CacheWrite: 500, TotalTokens: 8500, Cost: wireUsageCost{}}},
		{"large", wireUsage{Input: 100000, Output: 8000, CacheRead: 50000, CacheWrite: 10000, TotalTokens: 168000, Cost: wireUsageCost{}}},
		{"max_context", wireUsage{Input: 200000, Output: 8192, CacheRead: 0, CacheWrite: 0, TotalTokens: 208192, Cost: wireUsageCost{}}},
		{"cache_heavy", wireUsage{Input: 1000, Output: 500, CacheRead: 100000, CacheWrite: 50000, TotalTokens: 151500, Cost: wireUsageCost{}}},
		{"output_only", wireUsage{Input: 0, Output: 8192, CacheRead: 0, CacheWrite: 0, TotalTokens: 8192, Cost: wireUsageCost{}}},
	}

	for _, model := range models {
		for _, u := range usages {
			corpus = append(corpus, costTestCase{
				Name:  fmt.Sprintf("%s_%s", model.ID, u.name),
				Model: model,
				Usage: u.usage,
			})
		}
	}

	return corpus
}

// buildOverflowCorpus generates diverse overflow detection test cases.
func buildOverflowCorpus() []overflowTestCase {
	var corpus []overflowTestCase

	// Error messages that SHOULD be detected as overflow (shared patterns).
	overflowErrors := []string{
		"prompt is too long: 213462 tokens > 200000 maximum",
		"Your input is too long for requested model",
		"Your input exceeds the context window of this model",
		"The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens",
		"Please reduce the length of the messages or completion",
		"This endpoint's maximum context length is 131072 tokens",
		"prompt token count of 150000 exceeds the limit of 128000",
		"the request exceeds the available context size, try increasing it",
		"tokens to keep from the initial prompt is greater than the context length",
		"invalid params, context window exceeds limit",
		"context_length_exceeded",
		"context length exceeded: too many input tokens",
		"too many tokens in the request",
		"token limit exceeded for this model",
	}

	for i, errMsg := range overflowErrors {
		corpus = append(corpus, overflowTestCase{
			Name: fmt.Sprintf("overflow_pattern_%d", i),
			Message: wireMessage{
				Role:         "assistant",
				Content:      []wireContent{},
				StopReason:   "error",
				ErrorMessage: errMsg,
				Usage:        &wireUsage{Input: 200000, Output: 0, TotalTokens: 200000, Cost: wireUsageCost{}},
				Timestamp:    1710000000000,
			},
			ContextWindow: 0,
		})
	}

	// Empty body 4xx patterns (shared between Go and TS).
	emptyBodyPatterns := []string{
		"400 status code (no body)",
		"400 (no body)",
		"413 status code (no body)",
		"413 (no body)",
	}
	for i, errMsg := range emptyBodyPatterns {
		corpus = append(corpus, overflowTestCase{
			Name: fmt.Sprintf("empty_body_%d", i),
			Message: wireMessage{
				Role:         "assistant",
				Content:      []wireContent{},
				StopReason:   "error",
				ErrorMessage: errMsg,
				Usage:        &wireUsage{Cost: wireUsageCost{}},
				Timestamp:    1710000000000,
			},
			ContextWindow: 0,
		})
	}

	// Silent overflow (usage exceeds context window).
	corpus = append(corpus, overflowTestCase{
		Name: "silent_overflow_usage_exceeds",
		Message: wireMessage{
			Role:       "assistant",
			Content:    []wireContent{{Type: "text", Text: "response"}},
			StopReason: "stop",
			Usage:      &wireUsage{Input: 150000, Output: 100, CacheRead: 60000, TotalTokens: 210100, Cost: wireUsageCost{}},
			Timestamp:  1710000000000,
		},
		ContextWindow: 200000,
	})

	// NOT overflow cases.
	notOverflow := []struct {
		name    string
		msg     wireMessage
		ctxWin  int
	}{
		{"normal_stop", wireMessage{
			Role: "assistant", Content: []wireContent{{Type: "text", Text: "hi"}},
			StopReason: "stop", Usage: &wireUsage{Input: 100, Output: 50, TotalTokens: 150, Cost: wireUsageCost{}},
			Timestamp: 1710000000000,
		}, 200000},
		{"error_not_overflow", wireMessage{
			Role: "assistant", Content: []wireContent{},
			StopReason: "error", ErrorMessage: "internal server error",
			Usage: &wireUsage{Cost: wireUsageCost{}}, Timestamp: 1710000000000,
		}, 0},
		{"length_stop", wireMessage{
			Role: "assistant", Content: []wireContent{{Type: "text", Text: "long output..."}},
			StopReason: "length", Usage: &wireUsage{Input: 1000, Output: 8192, TotalTokens: 9192, Cost: wireUsageCost{}},
			Timestamp: 1710000000000,
		}, 200000},
		{"usage_within_window", wireMessage{
			Role: "assistant", Content: []wireContent{{Type: "text", Text: "ok"}},
			StopReason: "stop", Usage: &wireUsage{Input: 50000, Output: 100, CacheRead: 10000, TotalTokens: 60100, Cost: wireUsageCost{}},
			Timestamp: 1710000000000,
		}, 200000},
		{"tool_use_stop", wireMessage{
			Role: "assistant", Content: []wireContent{{Type: "toolCall", ID: "tc1", Name: "search", Arguments: map[string]any{"q": "test"}}},
			StopReason: "toolUse", Usage: &wireUsage{Input: 500, Output: 200, TotalTokens: 700, Cost: wireUsageCost{}},
			Timestamp: 1710000000000,
		}, 128000},
	}
	for _, tc := range notOverflow {
		corpus = append(corpus, overflowTestCase{
			Name:          "not_overflow_" + tc.name,
			Message:       tc.msg,
			ContextWindow: tc.ctxWin,
		})
	}

	return corpus
}

// =============================================================================
// O1: TransformMessages Comparison Oracle
// =============================================================================

func TestO1_TransformMessagesVsTypeScript(t *testing.T) {
	if testing.Short() {
		t.Skip("comparison oracle requires Node.js")
	}

	harness := newTSHarness(t)
	corpus := buildTransformCorpus()

	if len(corpus) < 100 {
		t.Fatalf("corpus too small: %d test cases (need 100+)", len(corpus))
	}

	for _, tc := range corpus {
		t.Run(tc.Name, func(t *testing.T) {
			// Run Go implementation.
			goMsgs := make([]Message, len(tc.Messages))
			for i, wm := range tc.Messages {
				goMsgs[i] = wireMsgToGo(wm)
			}
			goModel := Model{
				ID:            tc.TargetModel.ID,
				Name:          tc.TargetModel.Name,
				API:           tc.TargetModel.API,
				Provider:      tc.TargetModel.Provider,
				BaseURL:       tc.TargetModel.BaseURL,
				Reasoning:     tc.TargetModel.Reasoning,
				Input:         tc.TargetModel.Input,
				Cost:          ModelCost{Input: tc.TargetModel.Cost.Input, Output: tc.TargetModel.Cost.Output, CacheRead: tc.TargetModel.Cost.CacheRead, CacheWrite: tc.TargetModel.Cost.CacheWrite},
				ContextWindow: tc.TargetModel.ContextWindow,
				MaxTokens:     tc.TargetModel.MaxTokens,
			}

			goResult := TransformMessages(goMsgs, goModel, nil)
			goWire := goMsgsToWire(goResult)

			// Run TS implementation.
			tsWire := harness.transformMessages(t, tc.Messages, tc.TargetModel)

			// Compare (ignoring Timestamp on synthetic ToolResultMessages).
			compareTransformResults(t, goWire, tsWire)
		})
	}
}

// compareTransformResults compares Go and TS transform results, ignoring
// timestamps on synthetic ToolResultMessages (which use time.Now()/Date.now()).
func compareTransformResults(t *testing.T, goResult, tsResult []wireMessage) {
	t.Helper()

	if len(goResult) != len(tsResult) {
		goJSON, _ := json.MarshalIndent(goResult, "", "  ")
		tsJSON, _ := json.MarshalIndent(tsResult, "", "  ")
		t.Fatalf("message count mismatch: Go=%d, TS=%d\nGo:\n%s\nTS:\n%s",
			len(goResult), len(tsResult), goJSON, tsJSON)
	}

	for i := range goResult {
		goMsg := goResult[i]
		tsMsg := tsResult[i]

		// Zero out timestamps on synthetic tool results (they use Now()).
		if isSyntheticToolResult(goMsg) || isSyntheticToolResult(tsMsg) {
			goMsg.Timestamp = 0
			tsMsg.Timestamp = 0
		}

		goJSON, _ := json.Marshal(goMsg)
		tsJSON, _ := json.Marshal(tsMsg)

		if string(goJSON) != string(tsJSON) {
			t.Errorf("message[%d] mismatch:\n  Go: %s\n  TS: %s", i, goJSON, tsJSON)
		}
	}
}

func isSyntheticToolResult(msg wireMessage) bool {
	if msg.Role != "toolResult" {
		return false
	}
	if msg.IsError == nil || !*msg.IsError {
		return false
	}
	if len(msg.Content) == 1 && msg.Content[0].Type == "text" && msg.Content[0].Text == "No result provided" {
		return true
	}
	return false
}

// =============================================================================
// O2: CalculateCost Comparison Oracle
// =============================================================================

func TestO2_CalculateCostVsTypeScript(t *testing.T) {
	if testing.Short() {
		t.Skip("comparison oracle requires Node.js")
	}

	harness := newTSHarness(t)
	corpus := buildCostCorpus()

	for _, tc := range corpus {
		t.Run(tc.Name, func(t *testing.T) {
			// Run Go implementation.
			goModel := Model{
				ID:       tc.Model.ID,
				Provider: tc.Model.Provider,
				Cost:     ModelCost{Input: tc.Model.Cost.Input, Output: tc.Model.Cost.Output, CacheRead: tc.Model.Cost.CacheRead, CacheWrite: tc.Model.Cost.CacheWrite},
			}
			goUsage := &Usage{
				Input:       tc.Usage.Input,
				Output:      tc.Usage.Output,
				CacheRead:   tc.Usage.CacheRead,
				CacheWrite:  tc.Usage.CacheWrite,
				TotalTokens: tc.Usage.TotalTokens,
			}
			CalculateCost(goModel, goUsage)

			// Run TS implementation.
			tsCost := harness.calculateCost(t, tc.Model, tc.Usage)

			// Compare with floating point tolerance.
			const epsilon = 1e-12
			if !floatClose(goUsage.Cost.Input, tsCost.Input, epsilon) {
				t.Errorf("cost.Input: Go=%v, TS=%v", goUsage.Cost.Input, tsCost.Input)
			}
			if !floatClose(goUsage.Cost.Output, tsCost.Output, epsilon) {
				t.Errorf("cost.Output: Go=%v, TS=%v", goUsage.Cost.Output, tsCost.Output)
			}
			if !floatClose(goUsage.Cost.CacheRead, tsCost.CacheRead, epsilon) {
				t.Errorf("cost.CacheRead: Go=%v, TS=%v", goUsage.Cost.CacheRead, tsCost.CacheRead)
			}
			if !floatClose(goUsage.Cost.CacheWrite, tsCost.CacheWrite, epsilon) {
				t.Errorf("cost.CacheWrite: Go=%v, TS=%v", goUsage.Cost.CacheWrite, tsCost.CacheWrite)
			}
			if !floatClose(goUsage.Cost.Total, tsCost.Total, epsilon) {
				t.Errorf("cost.Total: Go=%v, TS=%v", goUsage.Cost.Total, tsCost.Total)
			}
		})
	}
}

func floatClose(a, b, epsilon float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) < epsilon
}

// =============================================================================
// O3: IsContextOverflow Comparison Oracle
// =============================================================================

func TestO3_IsContextOverflowVsTypeScript(t *testing.T) {
	if testing.Short() {
		t.Skip("comparison oracle requires Node.js")
	}

	harness := newTSHarness(t)
	corpus := buildOverflowCorpus()

	for _, tc := range corpus {
		t.Run(tc.Name, func(t *testing.T) {
			// Run Go implementation.
			goMsg := wireMsgToGo(tc.Message).(*AssistantMessage)
			goResult := IsContextOverflow(goMsg, tc.ContextWindow)

			// Run TS implementation.
			tsResult := harness.isContextOverflow(t, tc.Message, tc.ContextWindow)

			if goResult != tsResult {
				t.Errorf("mismatch: Go=%v, TS=%v (error=%q)", goResult, tsResult, tc.Message.ErrorMessage)
			}
		})
	}
}

// =============================================================================
// Corpus file helpers (for CI integration)
// =============================================================================

// writeCorpusFile writes the transform corpus to testdata/transform_corpus.json.
// Can be called from TestMain or go generate.
func writeCorpusFile(t *testing.T) string {
	t.Helper()
	corpus := buildTransformCorpus()

	_, thisFile, _, _ := runtime.Caller(0)
	corpusPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "transform_corpus.json")

	data, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		t.Fatalf("marshal corpus: %v", err)
	}
	if err := os.WriteFile(corpusPath, data, 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	t.Logf("wrote %d test cases to %s", len(corpus), corpusPath)
	return corpusPath
}

// loadTestCorpus loads the transform corpus from testdata/transform_corpus.json.
// Regenerates the corpus if the file is missing or stale (older than this source file).
func loadTestCorpus(t *testing.T) []transformTestCase {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	corpusPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "transform_corpus.json")

	needsRegen := false
	corpusStat, err := os.Stat(corpusPath)
	if err != nil {
		needsRegen = true
	} else if srcStat, err := os.Stat(thisFile); err == nil && srcStat.ModTime().After(corpusStat.ModTime()) {
		t.Log("corpus file is stale (older than oracle_test.go), regenerating")
		needsRegen = true
	}

	if needsRegen {
		writeCorpusFile(t)
	}

	data, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	var corpus []transformTestCase
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("unmarshal corpus: %v", err)
	}
	return corpus
}

// TestCorpusSize verifies the corpus meets the 100+ minimum.
func TestCorpusSize(t *testing.T) {
	corpus := buildTransformCorpus()
	if len(corpus) < 100 {
		t.Errorf("transform corpus has %d cases, need at least 100", len(corpus))
	}
	t.Logf("transform corpus: %d test cases", len(corpus))

	costCorpus := buildCostCorpus()
	t.Logf("cost corpus: %d test cases", len(costCorpus))

	overflowCorpus := buildOverflowCorpus()
	t.Logf("overflow corpus: %d test cases", len(overflowCorpus))
}

// TestWriteCorpusFile generates the corpus file for external consumers.
func TestWriteCorpusFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping corpus file generation in short mode")
	}
	path := writeCorpusFile(t)
	_ = path

	// Verify it's loadable.
	corpus := loadTestCorpus(t)
	if len(corpus) < 100 {
		t.Fatalf("loaded corpus too small: %d", len(corpus))
	}
}

// =============================================================================
// Helpers
// =============================================================================

// nodeAvailable checks if Node.js is available.
func nodeAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

