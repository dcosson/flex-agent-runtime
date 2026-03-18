//go:build integration

package orchestrator_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	rpcclient "github.com/dcosson/flex-agent-runtime/internal/rpc/client"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

// TestOrchestratorCreateSendDestroySkeleton is a gated skeleton for real
// orchestrator integration runs. It is intentionally skipped unless the target
// endpoint is provided.
func TestOrchestratorCreateSendDestroySkeleton(t *testing.T) {
	baseAddr := strings.TrimSpace(os.Getenv("ORCHESTRATOR_INTEGRATION_ADDR"))
	if baseAddr == "" {
		t.Skip("set ORCHESTRATOR_INTEGRATION_ADDR=host:port to run integration skeleton")
	}

	cfg := transport.ClientConfig{}
	if token := strings.TrimSpace(os.Getenv("ORCHESTRATOR_INTEGRATION_AUTH_TOKEN")); token != "" {
		cfg.HeaderInjector = transport.HeaderTokenAuth("authorization", "Bearer "+token)
	}
	client := rpcclient.NewAgentServiceClient(http.DefaultClient, control.AddressToURL(baseAddr), cfg)
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	createResp, err := client.CreateSession(ctx, &agentapi.CreateAgentSessionRequest{
		SessionConfig: agentapi.SessionConfig{
			Metadata: map[string]any{"placement_mode": "tools-sandbox"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = client.DestroySession(context.Background(), &agentapi.DestroyAgentSessionRequest{SessionID: createResp.SessionID})
	})

	recv, err := client.SendMessage(ctx, &agentapi.SendMessageRequest{
		SessionID: createResp.SessionID,
		Message:   "integration skeleton ping",
	})
	if err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	_ = recv.Close()

	if _, err := client.DestroySession(ctx, &agentapi.DestroyAgentSessionRequest{SessionID: createResp.SessionID}); err != nil {
		t.Fatalf("DestroySession() error = %v", err)
	}
}
