package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type mockAPIClient struct {
	clientType string
}

func (m *mockAPIClient) ClientType() string { return m.clientType }

func (m *mockAPIClient) Stream(_ context.Context, _ ProviderEndpoint, _ Model,
	_ Context, _ StreamOptions) *EventStream {
	return nil
}

func (m *mockAPIClient) StreamSimple(_ context.Context, _ ProviderEndpoint, _ Model,
	_ Context, _ SimpleStreamOptions) *EventStream {
	return nil
}

func TestAPIClientRegistryBasic(t *testing.T) {
	ClearAPIClients()
	t.Cleanup(ClearAPIClients)

	c := &mockAPIClient{clientType: "openai-completions"}
	RegisterAPIClient(c)

	got, err := GetAPIClient("openai-completions")
	if err != nil {
		t.Fatalf("GetAPIClient err: %v", err)
	}
	if got.ClientType() != "openai-completions" {
		t.Fatalf("type=%q", got.ClientType())
	}

	_, err = GetAPIClient("missing")
	if err == nil {
		t.Fatal("expected error for missing client")
	}

	ClearAPIClients()
	_, err = GetAPIClient("openai-completions")
	if err == nil {
		t.Fatal("expected error after clear")
	}
}

func TestAPIClientRegistryConcurrent(t *testing.T) {
	ClearAPIClients()
	t.Cleanup(ClearAPIClients)

	const goroutines = 20
	const ops = 500
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				ct := fmt.Sprintf("type-%d", j%10)
				switch j % 3 {
				case 0:
					RegisterAPIClient(&mockAPIClient{clientType: ct})
				case 1:
					_, _ = GetAPIClient(ct)
				case 2:
					ClearAPIClients()
				}
			}
		}(i)
	}
	wg.Wait()
}
