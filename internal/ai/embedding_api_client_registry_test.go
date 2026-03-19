package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type mockEmbeddingAPIClient struct {
	clientType string
}

func (m *mockEmbeddingAPIClient) ClientType() string { return m.clientType }

func (m *mockEmbeddingAPIClient) Embed(_ context.Context, _ ProviderEndpoint,
	_ EmbeddingModel, _ EmbeddingRequest) (*EmbeddingResponse, error) {
	return nil, nil
}

func TestEmbeddingAPIClientRegistryBasic(t *testing.T) {
	ClearEmbeddingAPIClients()
	t.Cleanup(ClearEmbeddingAPIClients)

	c := &mockEmbeddingAPIClient{clientType: "openai-embeddings"}
	RegisterEmbeddingAPIClient(c)

	got, err := GetEmbeddingAPIClient("openai-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingAPIClient err: %v", err)
	}
	if got.ClientType() != "openai-embeddings" {
		t.Fatalf("type=%q", got.ClientType())
	}

	_, err = GetEmbeddingAPIClient("missing")
	if err == nil {
		t.Fatal("expected error for missing client")
	}

	ClearEmbeddingAPIClients()
	_, err = GetEmbeddingAPIClient("openai-embeddings")
	if err == nil {
		t.Fatal("expected error after clear")
	}
}

func TestEmbeddingAPIClientRegistryConcurrent(t *testing.T) {
	ClearEmbeddingAPIClients()
	t.Cleanup(ClearEmbeddingAPIClients)

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
					RegisterEmbeddingAPIClient(&mockEmbeddingAPIClient{clientType: ct})
				case 1:
					_, _ = GetEmbeddingAPIClient(ct)
				case 2:
					ClearEmbeddingAPIClients()
				}
			}
		}(i)
	}
	wg.Wait()
}
