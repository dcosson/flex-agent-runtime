// Package main provides a minimal terminal chat client for the flexagent
// orchestrator RPC endpoint.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	rpcclient "github.com/dcosson/flex-agent-runtime/internal/rpc/client"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
)

type config struct {
	addr       string
	authToken  string
	sessionID  string
	driver     string
	provider   string
	model      string
	system     string
	toolsCSV   string
	localRoot  string
	keepOnExit bool
}

func main() {
	cfg := parseFlags()

	baseURL := normalizeBaseURL(cfg.addr)
	transportCfg := transport.ClientConfig{
		APIVersion: "v1",
	}
	if header, ok := authHeaderValue(cfg.authToken); ok {
		transportCfg.HeaderInjector = transport.HeaderTokenAuth("authorization", header)
	}

	client := rpcclient.NewAgentServiceClient(&http.Client{}, baseURL, transportCfg)
	defer func() { _ = client.Close() }()

	sessionResp, err := client.CreateSession(context.Background(), &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			SessionID:    cfg.sessionID,
			Driver:       cfg.driver,
			Provider:     cfg.provider,
			Model:        cfg.model,
			SystemPrompt: cfg.system,
			Tools:        parseTools(cfg.toolsCSV),
			ToolEnvironment: agentapi.ToolEnvironmentConfig{
				Type:         agentapi.ToolEnvLocal,
				LocalRootDir: cfg.localRoot,
			},
		},
	})
	if err != nil {
		fatalf("create session: %v", err)
	}
	sessionID := sessionResp.SessionID
	fmt.Printf("session created: %s\n", sessionID)

	if !cfg.keepOnExit {
		defer func() {
			_, _ = client.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{
				SessionID: sessionID,
			})
		}()
	}

	fmt.Println("type a message and press Enter. commands: /quit /exit /continue")
	runREPL(client, sessionID)
}

func parseFlags() config {
	cfg := config{}

	flag.StringVar(&cfg.addr, "addr", "localhost:8080", "orchestrator RPC address")
	flag.StringVar(&cfg.authToken, "auth-token", "", "auth token for authorization header")
	flag.StringVar(&cfg.sessionID, "session-id", "", "optional session ID (server generates one when empty)")
	flag.StringVar(&cfg.driver, "driver", envOrDefault("FLEXCHAT_DRIVER", ""), "agent driver")
	flag.StringVar(&cfg.provider, "provider", envOrDefault("FLEXCHAT_PROVIDER", ""), "model provider")
	flag.StringVar(&cfg.model, "model", envOrDefault("FLEXCHAT_MODEL", ""), "model ID")
	flag.StringVar(&cfg.system, "system-prompt", envOrDefault("FLEXCHAT_SYSTEM_PROMPT", ""), "system prompt")
	flag.StringVar(&cfg.toolsCSV, "tools", envOrDefault("FLEXCHAT_TOOLS", ""), "comma-separated tool names")
	flag.StringVar(&cfg.localRoot, "local-root", envOrDefault("FLEXCHAT_LOCAL_ROOT", ""), "ToolEnvironment local root dir")
	flag.BoolVar(&cfg.keepOnExit, "keep-session", false, "do not destroy session on exit")
	flag.Parse()

	return cfg
}

func runREPL(client *rpcclient.AgentServiceClient, sessionID string) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			fmt.Println()
			return
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch line {
		case "/quit", "/exit":
			return
		case "/continue":
			recv, err := client.Continue(context.Background(), &agentapi.ContinueRequest{
				SessionID: sessionID,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "continue error: %v\n", err)
				continue
			}
			streamEvents(recv)
		default:
			recv, err := client.SendMessage(context.Background(), &agentapi.SendMessageRequest{
				SessionID: sessionID,
				Message:   line,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "send error: %v\n", err)
				continue
			}
			streamEvents(recv)
		}
	}
}

func streamEvents(recv agentapi.EventReceiver) {
	defer func() { _ = recv.Close() }()

	var assistantLineOpen bool
	for {
		evt, err := recv.Recv()
		if errors.Is(err, io.EOF) {
			if assistantLineOpen {
				fmt.Println()
			}
			return
		}
		if err != nil {
			if assistantLineOpen {
				fmt.Println()
			}
			fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
			return
		}

		switch evt.Type {
		case agentapi.EventAgentMessageDelta:
			if !assistantLineOpen {
				fmt.Print("assistant> ")
				assistantLineOpen = true
			}
			fmt.Print(evt.Delta)
		case agentapi.EventAgentMessageCompleted:
			if assistantLineOpen {
				fmt.Println()
				assistantLineOpen = false
			}
			fmt.Printf("[event] %s\n", evt.Type)
		case agentapi.EventThinkingDelta:
			if assistantLineOpen {
				fmt.Println()
				assistantLineOpen = false
			}
			fmt.Printf("[thinking] %s\n", evt.Delta)
		default:
			if assistantLineOpen {
				fmt.Println()
				assistantLineOpen = false
			}
			fmt.Printf("[event] %s", evt.Type)
			if evt.ToolName != "" {
				fmt.Printf(" tool=%s", evt.ToolName)
			}
			if evt.ErrorMessage != "" {
				fmt.Printf(" error=%s", evt.ErrorMessage)
			}
			if evt.ControlMessage != "" {
				fmt.Printf(" msg=%s", evt.ControlMessage)
			}
			fmt.Println()
		}
	}
}

func parseTools(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}

	parts := strings.Split(csv, ",")
	tools := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		tools = append(tools, name)
	}
	if len(tools) == 0 {
		return nil
	}
	return tools
}

func authHeaderValue(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return token, true
	}
	return "Bearer " + token, true
}

func normalizeBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + strings.TrimRight(addr, "/")
}

func envOrDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
