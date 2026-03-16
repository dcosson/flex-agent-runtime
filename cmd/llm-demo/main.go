package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/ai/provider/anthropic"
	"h2-agent-runtime/internal/ai/provider/google"
	"h2-agent-runtime/internal/ai/provider/openai"
)

type chatConfig struct {
	Provider string
	Model    string
	Timeout  time.Duration
}

func main() {
	cfg := parseFlags()

	registerChatProviders()

	model, err := resolveChatModel(cfg.Provider, cfg.Model)
	if err != nil {
		fatalf("resolve model: %v", err)
	}

	fmt.Printf("llm-demo started (provider=%s model=%s)\n", model.Provider, model.ID)
	fmt.Println("Type a prompt and press enter. Use /exit to quit.")

	history := make([]ai.Message, 0, 64)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("you> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fatalf("read stdin: %v", err)
			}
			fmt.Println()
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return
		}

		history = append(history, &ai.UserMessage{
			Content:   []ai.ContentBlock{&ai.TextContent{Text: line}},
			Timestamp: ai.TimeToMillis(time.Now()),
		})

		if err := runAssistantTurn(model, &history, cfg.Timeout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}

func parseFlags() chatConfig {
	provider := envOrDefault("LLM_PROVIDER", "anthropic")
	model := os.Getenv("LLM_MODEL")
	timeout := flag.Duration("timeout", 90*time.Second, "per-request timeout")
	flag.StringVar(&provider, "provider", provider, "provider: anthropic|openai|google")
	flag.StringVar(&model, "model", model, "model ID (optional)")
	flag.Parse()
	return chatConfig{Provider: strings.TrimSpace(provider), Model: strings.TrimSpace(model), Timeout: *timeout}
}

func runAssistantTurn(model ai.Model, history *[]ai.Message, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for attempts := 0; attempts < 8; attempts++ {
		llmCtx := ai.Context{
			Messages: ai.TransformMessages(*history, model, nil),
			Tools:    []ai.Tool{calculatorTool()},
		}

		es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
		for range es.C {
		}
		msg, err := es.Result()
		if err != nil {
			return err
		}
		*history = append(*history, &msg)

		toolCalls := assistantToolCalls(msg)
		if len(toolCalls) == 0 {
			fmt.Printf("assistant> %s\n", assistantText(msg))
			return nil
		}

		for _, tc := range toolCalls {
			resultText, toolErr := executeCalculatorTool(tc)
			if toolErr != nil {
				resultText = "error: " + toolErr.Error()
			}
			fmt.Printf("tool[%s]> %s\n", tc.Name, resultText)

			*history = append(*history, &ai.ToolResultMessage{
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    []ai.ContentBlock{&ai.TextContent{Text: resultText}},
				IsError:    toolErr != nil,
				Timestamp:  ai.TimeToMillis(time.Now()),
			})
		}
	}

	return errors.New("tool loop exceeded 8 iterations")
}

func calculatorTool() ai.Tool {
	return ai.Tool{
		Name:        "calculator",
		Description: "Evaluate arithmetic expressions using +, -, *, /, and parentheses.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","description":"Arithmetic expression to evaluate."}},"required":["expression"]}`),
	}
}

func executeCalculatorTool(tc *ai.ToolCall) (string, error) {
	if tc == nil {
		return "", errors.New("nil tool call")
	}
	if tc.Name != "calculator" {
		return "", fmt.Errorf("unsupported tool %q", tc.Name)
	}
	raw, ok := tc.Arguments["expression"]
	if !ok {
		return "", errors.New("missing argument: expression")
	}
	expr := strings.TrimSpace(fmt.Sprint(raw))
	if expr == "" {
		return "", errors.New("expression is empty")
	}
	v, err := EvalExpression(expr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s = %g", expr, v), nil
}

func assistantToolCalls(msg ai.AssistantMessage) []*ai.ToolCall {
	out := make([]*ai.ToolCall, 0)
	for _, block := range msg.Content {
		if tc, ok := block.(*ai.ToolCall); ok {
			out = append(out, tc)
		}
	}
	return out
}

func assistantText(msg ai.AssistantMessage) string {
	parts := make([]string, 0)
	for _, block := range msg.Content {
		if t, ok := block.(*ai.TextContent); ok && strings.TrimSpace(t.Text) != "" {
			parts = append(parts, t.Text)
		}
	}
	if len(parts) == 0 {
		return "(no text response)"
	}
	return strings.Join(parts, "\n")
}

func resolveChatModel(provider, modelID string) (ai.Model, error) {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" {
		p = "anthropic"
	}
	if modelID == "" {
		modelID = defaultChatModelID(p)
	}
	if m, err := ai.GetModel(p, modelID); err == nil {
		return m, nil
	}
	if m, ok := fallbackChatModels()[p][modelID]; ok {
		return m, nil
	}
	return ai.Model{}, fmt.Errorf("unknown model %q for provider %q", modelID, p)
}

func defaultChatModelID(provider string) string {
	switch provider {
	case "openai":
		return "gpt-4o"
	case "google":
		return "gemini-2.5-flash"
	default:
		return "claude-sonnet-4-20250514"
	}
}

func fallbackChatModels() map[string]map[string]ai.Model {
	return map[string]map[string]ai.Model{
		"anthropic": {
			"claude-sonnet-4-20250514": {
				ID:       "claude-sonnet-4-20250514",
				API:      "anthropic-messages",
				Provider: "anthropic",
			},
		},
		"openai": {
			"gpt-4o": {
				ID:       "gpt-4o",
				API:      "openai-completions",
				Provider: "openai",
			},
		},
		"google": {
			"gemini-2.5-flash": {
				ID:       "gemini-2.5-flash",
				API:      "google-genai",
				Provider: "google",
			},
		},
	}
}

func registerChatProviders() {
	openai.Register(openai.Config{
		APIKey:  os.Getenv("OPENAI_API_KEY"),
		BaseURL: os.Getenv("OPENAI_BASE_URL"),
	}, "cmd-llm-demo")

	google.Register(google.Config{
		APIKey:  os.Getenv("GOOGLE_API_KEY"),
		BaseURL: os.Getenv("GOOGLE_BASE_URL"),
		Version: os.Getenv("GOOGLE_API_VERSION"),
	}, "cmd-llm-demo")

	anthropic.Register(anthropic.Config{
		APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		Version: os.Getenv("ANTHROPIC_VERSION"),
	}, "cmd-llm-demo")
}

func envOrDefault(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
