package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dcosson/flex-agent-runtime/ai"
	provideranthropic "github.com/dcosson/flex-agent-runtime/ai/provider/anthropic"
	providergoogle "github.com/dcosson/flex-agent-runtime/ai/provider/google"
	provideropenai "github.com/dcosson/flex-agent-runtime/ai/provider/openai"
)

const (
	anthropicProvider = "anthropic"
	openaiProvider    = "openai"
	googleProvider    = "google"
)

type chatConfig struct {
	Provider string
	Model    string
	Timeout  time.Duration
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "llm-demo",
		Short: "Interactive LLM demo with calculator tool-use",
		Long: strings.Join([]string{
			"Interactive LLM demo with calculator tool-use.",
			"",
			"API key env vars:",
			"  ANTHROPIC_API_KEY",
			"  OPENAI_API_KEY",
			"  GOOGLE_API_KEY",
			"",
			"Base URL env vars (optional, for compatible endpoints):",
			"  ANTHROPIC_BASE_URL",
			"  OPENAI_BASE_URL",
			"  GOOGLE_BASE_URL",
			"  GOOGLE_API_VERSION",
		}, "\n"),
	}
	root.SilenceUsage = true
	root.CompletionOptions.DisableDefaultCmd = false

	root.AddCommand(newChatCmd())
	root.InitDefaultCompletionCmd()
	return root
}

func newChatCmd() *cobra.Command {
	provider := envOrDefault("LLM_PROVIDER", anthropicProvider)
	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	timeout := 90 * time.Second

	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Run interactive stdin chat loop",
		Long: strings.Join([]string{
			"Run an interactive stdin chat loop with a built-in calculator tool.",
			"",
			"API key env vars:",
			"  ANTHROPIC_API_KEY",
			"  OPENAI_API_KEY",
			"  GOOGLE_API_KEY",
			"",
			"Base URL env vars (optional, for compatible endpoints):",
			"  ANTHROPIC_BASE_URL",
			"  OPENAI_BASE_URL",
			"  GOOGLE_BASE_URL",
			"  GOOGLE_API_VERSION",
		}, "\n"),
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg := chatConfig{
				Provider: strings.ToLower(strings.TrimSpace(provider)),
				Model:    strings.TrimSpace(model),
				Timeout:  timeout,
			}
			return runChat(cfg)
		},
	}
	cmd.Flags().StringVar(&provider, "provider", provider, "provider: anthropic|openai|google")
	cmd.Flags().StringVar(&model, "model", model, "model ID (optional)")
	cmd.Flags().DurationVar(&timeout, "timeout", timeout, "per-request timeout")
	return cmd
}

func runChat(cfg chatConfig) error {
	available := registerChatProviders()
	if len(available) == 0 {
		return fmt.Errorf("no API keys found; set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY")
	}

	if !contains(available, cfg.Provider) {
		return fmt.Errorf("provider %q requested but key is not set; available providers: %s", cfg.Provider, strings.Join(available, ", "))
	}

	model, err := resolveChatModel(cfg.Provider, cfg.Model)
	if err != nil {
		return fmt.Errorf("resolve model: %w", err)
	}

	fmt.Printf("llm-demo started (provider=%s model=%s)\n", model.Provider, model.ID)
	fmt.Printf("available providers: %s\n", strings.Join(available, ", "))
	fmt.Println("Type a prompt and press enter. Use /exit to quit.")

	history := make([]ai.Message, 0, 64)
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("you> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			fmt.Println()
			return nil
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			return nil
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

func runAssistantTurn(model ai.Model, history *[]ai.Message, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for attempts := 0; attempts < 8; attempts++ {
		llmCtx := ai.Context{
			Messages: ai.TransformMessages(*history, model, nil),
			Tools:    []ai.Tool{calculatorTool()},
		}

		es := ai.StreamSimple(ctx, model, llmCtx, ai.SimpleStreamOptions{})
		printedText := false
		streamedText := false
		printedThinking := false
		for ev := range es.C {
			switch ev.Type {
			case ai.EventThinkingDelta:
				if ev.Delta == "" {
					continue
				}
				if !printedThinking {
					fmt.Print("thinking> ")
					printedThinking = true
				}
				fmt.Print(ev.Delta)
			case ai.EventThinkingEnd:
				if printedThinking {
					fmt.Println()
					printedThinking = false
				}
			case ai.EventTextDelta:
				if ev.Delta == "" {
					continue
				}
				if !printedText {
					fmt.Print("assistant> ")
					printedText = true
				}
				streamedText = true
				fmt.Print(ev.Delta)
			case ai.EventTextEnd:
				if printedText {
					fmt.Println()
					printedText = false
				}
			}
		}
		msg, err := es.Result()
		if err != nil {
			return err
		}
		*history = append(*history, &msg)

		toolCalls := assistantToolCalls(msg)
		if len(toolCalls) == 0 {
			if !streamedText {
				fmt.Printf("assistant> %s\n", assistantText(msg))
			}
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
		p = anthropicProvider
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
	case openaiProvider:
		return "gpt-4o"
	case googleProvider:
		return "gemini-2.5-flash"
	default:
		return "claude-sonnet-4-20250514"
	}
}

func fallbackChatModels() map[string]map[string]ai.Model {
	return map[string]map[string]ai.Model{
		anthropicProvider: {
			"claude-sonnet-4-20250514": {
				ID:       "claude-sonnet-4-20250514",
				API:      "anthropic-messages",
				Provider: anthropicProvider,
			},
		},
		openaiProvider: {
			"gpt-4o": {
				ID:       "gpt-4o",
				API:      "openai-completions",
				Provider: openaiProvider,
			},
		},
		googleProvider: {
			"gemini-2.5-flash": {
				ID:       "gemini-2.5-flash",
				API:      "google-genai",
				Provider: googleProvider,
			},
		},
	}
}

func registerChatProviders() []string {
	available := make([]string, 0, 3)

	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		provideranthropic.Register(provideranthropic.Config{
			APIKey:  key,
			BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
			Version: os.Getenv("ANTHROPIC_VERSION"),
		}, "cmd-llm-demo")
		available = append(available, anthropicProvider)
	}

	if key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); key != "" {
		provideropenai.Register(provideropenai.Config{
			APIKey:  key,
			BaseURL: os.Getenv("OPENAI_BASE_URL"),
		}, "cmd-llm-demo")
		available = append(available, openaiProvider)
	}

	if key := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); key != "" {
		providergoogle.Register(providergoogle.Config{
			APIKey:  key,
			BaseURL: os.Getenv("GOOGLE_BASE_URL"),
			Version: os.Getenv("GOOGLE_API_VERSION"),
		}, "cmd-llm-demo")
		available = append(available, googleProvider)
	}

	sort.Strings(available)
	return available
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func envOrDefault(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}
