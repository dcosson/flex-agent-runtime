package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type mockEmbeddingClient struct {
	clientType string
	fn         EmbedFunc
}

func (m *mockEmbeddingClient) ClientType() string { return m.clientType }

func (m *mockEmbeddingClient) Embed(ctx context.Context, endpoint ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error) {
	if m.fn != nil {
		return m.fn(ctx, endpoint, model, req)
	}
	out := make([]Embedding, len(req.Texts))
	for i := range req.Texts {
		out[i] = Embedding{Index: i, Values: []float32{float32(i + 1)}}
	}
	return &EmbeddingResponse{Embeddings: out, Usage: EmbeddingUsage{Tokens: len(req.Texts)}}, nil
}

func TestEmbeddingConstants(t *testing.T) {
	tasks := []EmbeddingTaskType{
		EmbeddingTaskQuery,
		EmbeddingTaskDocument,
		EmbeddingTaskClassification,
		EmbeddingTaskClustering,
		EmbeddingTaskSimilarity,
		EmbeddingTaskUnspecified,
	}
	if len(tasks) != 6 {
		t.Fatalf("unexpected task type count: %d", len(tasks))
	}
	encs := []EmbeddingEncoding{
		EmbeddingEncodingFloat,
		EmbeddingEncodingBase64,
		EmbeddingEncodingInt8,
		EmbeddingEncodingUint8,
		EmbeddingEncodingBinary,
		EmbeddingEncodingUBinary,
	}
	if len(encs) != 6 {
		t.Fatalf("unexpected encoding count: %d", len(encs))
	}
}

func TestEmbeddingAPIClientRegistryBasicFromEmbeddingTest(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	c := &mockEmbeddingClient{clientType: "openai-embeddings"}
	RegisterEmbeddingAPIClient(c)
	got, err := GetEmbeddingAPIClient("openai-embeddings")
	if err != nil {
		t.Fatalf("GetEmbeddingAPIClient err: %v", err)
	}
	if got.ClientType() != c.clientType {
		t.Fatalf("unexpected client type: %q", got.ClientType())
	}
	ClearEmbeddingAPIClients()
	if _, err := GetEmbeddingAPIClient("openai-embeddings"); err == nil {
		t.Fatal("expected missing embedding client after clear")
	}
}

func TestEmbeddingAPIClientRegistryConcurrentFromEmbeddingTest(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	const goroutines = 20
	const ops = 500
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				ct := fmt.Sprintf("ct-%d", j%7)
				switch j % 3 {
				case 0:
					RegisterEmbeddingAPIClient(&mockEmbeddingClient{clientType: ct})
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

func TestGetEmbeddingAPIClientError(t *testing.T) {
	withIsolatedEmbeddingClients(t)
	_, err := GetEmbeddingAPIClient("missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}

func withIsolatedEmbeddingClients(t *testing.T) {
	t.Helper()
	ClearEmbeddingAPIClients()
	t.Cleanup(ClearEmbeddingAPIClients)
}

func withIsolatedEmbeddingModels(t *testing.T) {
	t.Helper()
	orig := ListEmbeddingModels()
	ClearEmbeddingModels()
	t.Cleanup(func() {
		ClearEmbeddingModels()
		for _, m := range orig {
			RegisterEmbeddingModel(m)
		}
	})
}

// registerMockEmbeddingProvider registers both an EmbeddingAPIClient and a ProviderConfig
// for use in tests that call Embed(). The clientType is used as the EmbeddingAPIClientType
// in the ProviderConfig, and providerName is the ProviderConfig name (matching model.Provider).
func registerMockEmbeddingProvider(t *testing.T, clientType, providerName string, fn EmbedFunc) {
	t.Helper()
	RegisterEmbeddingAPIClient(&mockEmbeddingClient{clientType: clientType, fn: fn})
	RegisterProviderConfig(ProviderConfig{
		Name:                   providerName,
		EmbeddingAPIClientType: clientType,
	})
	t.Cleanup(func() {
		UnregisterProviderConfig(providerName)
	})
}
