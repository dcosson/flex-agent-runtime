package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockStreamingAPIClient is a mock APIClient that returns a simple response.
type mockStreamingAPIClient struct {
	clientType string
}

func (m *mockStreamingAPIClient) ClientType() string { return m.clientType }
func (m *mockStreamingAPIClient) Stream(_ context.Context, _ ProviderEndpoint, _ Model, _ Context, _ StreamOptions) *EventStream {
	es := NewEventStream()
	go func() {
		defer es.Close()
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
	}()
	return es
}
func (m *mockStreamingAPIClient) StreamSimple(_ context.Context, _ ProviderEndpoint, _ Model, _ Context, _ SimpleStreamOptions) *EventStream {
	return m.Stream(context.Background(), ProviderEndpoint{}, Model{}, Context{}, StreamOptions{})
}

// P9: registry thread-safety under concurrent operations.
func TestRegistryConcurrentAccess(t *testing.T) {
	ClearAPIClients()
	ClearProviderConfigs()
	t.Cleanup(ClearAPIClients)
	t.Cleanup(ClearProviderConfigs)

	const goroutines = 50
	const ops = 1000

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				ct := fmt.Sprintf("ct-%d", id%10)
				pn := fmt.Sprintf("pn-%d", id%10)
				switch j % 5 {
				case 0:
					RegisterAPIClient(&mockAPIClient{clientType: ct})
				case 1:
					_, _ = GetAPIClient(ct)
				case 2:
					RegisterProviderConfig(ProviderConfig{Name: pn, APIClientType: ct})
				case 3:
					_, _ = GetProviderConfig(pn)
				case 4:
					UnregisterProviderConfig(pn)
				}
			}
		}(i)
	}
	wg.Wait()
}

// S2: stress registry under contention for a bounded duration.
func TestRegistryStressContention(t *testing.T) {
	ClearAPIClients()
	ClearProviderConfigs()
	t.Cleanup(ClearAPIClients)
	t.Cleanup(ClearProviderConfigs)

	deadline := time.Now().Add(750 * time.Millisecond)
	var wg sync.WaitGroup

	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			n := 0
			for time.Now().Before(deadline) {
				ct := fmt.Sprintf("ct-%d-%d", id, n%8)
				pn := fmt.Sprintf("pn-%d-%d", id, n%8)
				RegisterAPIClient(&mockAPIClient{clientType: ct})
				RegisterProviderConfig(ProviderConfig{Name: pn, APIClientType: ct})
				_, _ = GetAPIClient(ct)
				_, _ = GetProviderConfig(pn)
				n++
			}
		}(g)
	}
	wg.Wait()
}

func TestStreamEntryPoints(t *testing.T) {
	ClearAPIClients()
	ClearProviderConfigs()
	t.Cleanup(ClearAPIClients)
	t.Cleanup(ClearProviderConfigs)

	RegisterAPIClient(&mockStreamingAPIClient{clientType: "anthropic-messages"})
	RegisterProviderConfig(ProviderConfig{Name: "anthropic", APIClientType: "anthropic-messages"})

	model := Model{API: "anthropic-messages", Provider: "anthropic"}
	msg, err := Complete(context.Background(), model, Context{}, StreamOptions{})
	if err != nil {
		t.Fatalf("Complete err: %v", err)
	}
	if msg.Model != "ok" {
		t.Fatalf("unexpected model: %q", msg.Model)
	}

	missing := Model{API: "missing", Provider: "missing-provider"}
	es := Stream(context.Background(), missing, Context{}, StreamOptions{})
	_, err = es.Drain()
	if err == nil {
		t.Fatalf("expected error stream")
	}
}
