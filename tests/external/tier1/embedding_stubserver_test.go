package tier1

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/provider/cohere"
	"github.com/dcosson/flex-agent-runtime/internal/ai/provider/google"
	"github.com/dcosson/flex-agent-runtime/internal/ai/provider/openai"
	"github.com/dcosson/flex-agent-runtime/internal/ai/testutil/stubserver"
)

// loadJSONFixture reads a JSON fixture file from testdata/fixtures/.
func loadJSONFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(fixturesDir(t), name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(data)
}

func openaiEndpoint(srvURL, apiKey string) ai.ProviderEndpoint {
	return ai.ProviderEndpoint{ProviderName: "openai", BaseURL: srvURL, APIKey: apiKey}
}

func googleEndpoint(srvURL, apiKey string) ai.ProviderEndpoint {
	return ai.ProviderEndpoint{
		ProviderName:     "google",
		BaseURL:          srvURL,
		APIKey:           apiKey,
		ProviderSpecific: map[string]string{"apiVersion": "v1beta"},
	}
}

func cohereEndpoint(srvURL, apiKey string) ai.ProviderEndpoint {
	return ai.ProviderEndpoint{ProviderName: "cohere", BaseURL: srvURL, APIKey: apiKey}
}

// --- OpenAI Embedding Client ---

func TestEmbedding_OpenAI_Success(t *testing.T) {
	fixture := loadJSONFixture(t, "openai-embedding.json")
	srv := stubserver.NewJSONServer(fixture)
	defer srv.Close()

	c := openai.NewEmbeddingClient(openai.ClientConfig{})
	ep := openaiEndpoint(srv.URL, "test-key-openai-embed")
	model := ai.EmbeddingModel{ID: "text-embedding-3-small", MaxBatchSize: 100}

	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts: []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
	}
	if resp.Embeddings[0].Index != 0 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("unexpected indices: %d, %d", resp.Embeddings[0].Index, resp.Embeddings[1].Index)
	}
	if resp.Usage.Tokens != 10 {
		t.Fatalf("expected 10 tokens, got %d", resp.Usage.Tokens)
	}

	// Verify auth header was sent.
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer test-key-openai-embed" {
		t.Fatalf("auth header: %q", got)
	}
}

func TestEmbedding_OpenAI_Throttle429(t *testing.T) {
	srv := stubserver.NewJSONFaultServer("", stubserver.Fault{
		Mode:       stubserver.Throttle,
		StatusCode: 429,
		RetryAfter: "1",
	})
	defer srv.Close()

	c := openai.NewEmbeddingClient(openai.ClientConfig{})
	ep := openaiEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error")
	}
	var pErr *ai.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Code != ai.ErrRateLimit {
		t.Fatalf("expected ErrRateLimit, got %q", pErr.Code)
	}
}

func TestEmbedding_OpenAI_ServerError500(t *testing.T) {
	srv := stubserver.NewJSONFaultServer("", stubserver.Fault{
		Mode:       stubserver.EmptyBody,
		StatusCode: 500,
	})
	defer srv.Close()

	c := openai.NewEmbeddingClient(openai.ClientConfig{})
	ep := openaiEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error")
	}
	var pErr *ai.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Code != ai.ErrServerError {
		t.Fatalf("expected ErrServerError, got %q", pErr.Code)
	}
}

func TestEmbedding_OpenAI_MalformedJSON(t *testing.T) {
	fixture := loadJSONFixture(t, "openai-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:          stubserver.Malformed,
		MalformedData: `{"data": [{"embedding": [CORRUPTED`,
	})
	defer srv.Close()

	c := openai.NewEmbeddingClient(openai.ClientConfig{})
	ep := openaiEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error from malformed JSON")
	}
}

func TestEmbedding_OpenAI_TCPReset(t *testing.T) {
	fixture := loadJSONFixture(t, "openai-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:        stubserver.TCPReset,
		AfterEvents: 20, // 20 bytes then reset
	})
	defer srv.Close()

	c := openai.NewEmbeddingClient(openai.ClientConfig{})
	ep := openaiEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error from TCP reset")
	}
}

func TestEmbedding_OpenAI_Backpressure(t *testing.T) {
	fixture := loadJSONFixture(t, "openai-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:       stubserver.Backpressure,
		EventDelay: 50 * time.Millisecond,
	})
	defer srv.Close()

	c := openai.NewEmbeddingClient(openai.ClientConfig{})
	ep := openaiEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	start := time.Now()
	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) == 0 {
		t.Fatal("expected at least 1 embedding")
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("backpressure too fast: %v", elapsed)
	}
}

// --- Google Embedding Client ---

func TestEmbedding_Google_Success(t *testing.T) {
	fixture := loadJSONFixture(t, "google-embedding.json")
	srv := stubserver.NewJSONServer(fixture)
	defer srv.Close()

	c := google.NewEmbeddingClient(google.ClientConfig{})
	ep := googleEndpoint(srv.URL, "test-key-google-embed")
	model := ai.EmbeddingModel{ID: "gemini-embedding-001", MaxBatchSize: 100}

	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts: []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
	}
	if resp.Embeddings[0].Index != 0 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("unexpected indices: %d, %d", resp.Embeddings[0].Index, resp.Embeddings[1].Index)
	}

	// Google uses API key in query string, not Authorization header.
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
}

func TestEmbedding_Google_Throttle429(t *testing.T) {
	srv := stubserver.NewJSONFaultServer("", stubserver.Fault{
		Mode:       stubserver.Throttle,
		StatusCode: 429,
		RetryAfter: "2",
	})
	defer srv.Close()

	c := google.NewEmbeddingClient(google.ClientConfig{})
	ep := googleEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error")
	}
	var pErr *ai.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Code != ai.ErrRateLimit {
		t.Fatalf("expected ErrRateLimit, got %q", pErr.Code)
	}
}

func TestEmbedding_Google_ServerError500(t *testing.T) {
	srv := stubserver.NewJSONFaultServer("", stubserver.Fault{
		Mode:       stubserver.EmptyBody,
		StatusCode: 500,
	})
	defer srv.Close()

	c := google.NewEmbeddingClient(google.ClientConfig{})
	ep := googleEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error")
	}
	var pErr *ai.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Code != ai.ErrServerError {
		t.Fatalf("expected ErrServerError, got %q", pErr.Code)
	}
}

func TestEmbedding_Google_MalformedJSON(t *testing.T) {
	fixture := loadJSONFixture(t, "google-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:          stubserver.Malformed,
		MalformedData: `{"embeddings": [{CORRUPTED`,
	})
	defer srv.Close()

	c := google.NewEmbeddingClient(google.ClientConfig{})
	ep := googleEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error from malformed JSON")
	}
}

func TestEmbedding_Google_TCPReset(t *testing.T) {
	fixture := loadJSONFixture(t, "google-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:        stubserver.TCPReset,
		AfterEvents: 15,
	})
	defer srv.Close()

	c := google.NewEmbeddingClient(google.ClientConfig{})
	ep := googleEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	if err == nil {
		t.Fatal("expected error from TCP reset")
	}
}

func TestEmbedding_Google_Backpressure(t *testing.T) {
	fixture := loadJSONFixture(t, "google-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:       stubserver.Backpressure,
		EventDelay: 50 * time.Millisecond,
	})
	defer srv.Close()

	c := google.NewEmbeddingClient(google.ClientConfig{})
	ep := googleEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	start := time.Now()
	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{Texts: []string{"x"}})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) == 0 {
		t.Fatal("expected at least 1 embedding")
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("backpressure too fast: %v", elapsed)
	}
}

// --- Cohere Embedding Client ---

func TestEmbedding_Cohere_Success(t *testing.T) {
	fixture := loadJSONFixture(t, "cohere-embedding.json")
	srv := stubserver.NewJSONServer(fixture)
	defer srv.Close()

	c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
	ep := cohereEndpoint(srv.URL, "test-key-cohere-embed")
	model := ai.EmbeddingModel{ID: "embed-v3.5", MaxBatchSize: 96}

	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"hello", "world"},
		TaskType: ai.EmbeddingTaskQuery,
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(resp.Embeddings))
	}
	if resp.Embeddings[0].Index != 0 || resp.Embeddings[1].Index != 1 {
		t.Fatalf("unexpected indices: %d, %d", resp.Embeddings[0].Index, resp.Embeddings[1].Index)
	}
	if resp.Usage.Tokens != 2 {
		t.Fatalf("expected 2 tokens, got %d", resp.Usage.Tokens)
	}

	// Verify auth header.
	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer test-key-cohere-embed" {
		t.Fatalf("auth header: %q", got)
	}
}

func TestEmbedding_Cohere_Throttle429(t *testing.T) {
	srv := stubserver.NewJSONFaultServer("", stubserver.Fault{
		Mode:       stubserver.Throttle,
		StatusCode: 429,
	})
	defer srv.Close()

	c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
	ep := cohereEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"x"},
		TaskType: ai.EmbeddingTaskDocument,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var pErr *ai.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Code != ai.ErrRateLimit {
		t.Fatalf("expected ErrRateLimit, got %q", pErr.Code)
	}
}

func TestEmbedding_Cohere_ServerError500(t *testing.T) {
	srv := stubserver.NewJSONFaultServer("", stubserver.Fault{
		Mode:       stubserver.EmptyBody,
		StatusCode: 500,
	})
	defer srv.Close()

	c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
	ep := cohereEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"x"},
		TaskType: ai.EmbeddingTaskDocument,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	var pErr *ai.ProviderError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if pErr.Code != ai.ErrServerError {
		t.Fatalf("expected ErrServerError, got %q", pErr.Code)
	}
}

func TestEmbedding_Cohere_MalformedJSON(t *testing.T) {
	fixture := loadJSONFixture(t, "cohere-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:          stubserver.Malformed,
		MalformedData: `{"embeddings": {"float": [[CORRUPTED`,
	})
	defer srv.Close()

	c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
	ep := cohereEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"x"},
		TaskType: ai.EmbeddingTaskDocument,
	})
	if err == nil {
		t.Fatal("expected error from malformed JSON")
	}
}

func TestEmbedding_Cohere_TCPReset(t *testing.T) {
	fixture := loadJSONFixture(t, "cohere-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:        stubserver.TCPReset,
		AfterEvents: 25,
	})
	defer srv.Close()

	c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
	ep := cohereEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	_, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"x"},
		TaskType: ai.EmbeddingTaskDocument,
	})
	if err == nil {
		t.Fatal("expected error from TCP reset")
	}
}

func TestEmbedding_Cohere_Backpressure(t *testing.T) {
	fixture := loadJSONFixture(t, "cohere-embedding.json")
	srv := stubserver.NewJSONFaultServer(fixture, stubserver.Fault{
		Mode:       stubserver.Backpressure,
		EventDelay: 50 * time.Millisecond,
	})
	defer srv.Close()

	c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
	ep := cohereEndpoint(srv.URL, "")
	model := ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}

	start := time.Now()
	resp, err := c.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
		Texts:    []string{"x"},
		TaskType: ai.EmbeddingTaskDocument,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(resp.Embeddings) == 0 {
		t.Fatal("expected at least 1 embedding")
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("backpressure too fast: %v", elapsed)
	}
}

// --- Cross-provider auth header propagation ---

func TestEmbedding_AuthHeaderPropagation(t *testing.T) {
	fixture := loadJSONFixture(t, "openai-embedding.json")

	tests := []struct {
		name       string
		setupEmbed func(srvURL string) (ai.EmbeddingAPIClient, ai.ProviderEndpoint, ai.EmbeddingModel)
		wantHeader string
		wantValue  string
	}{
		{
			name: "openai_bearer",
			setupEmbed: func(srvURL string) (ai.EmbeddingAPIClient, ai.ProviderEndpoint, ai.EmbeddingModel) {
				c := openai.NewEmbeddingClient(openai.ClientConfig{})
				ep := openaiEndpoint(srvURL, "sk-openai-test")
				return c, ep, ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
			},
			wantHeader: "Authorization",
			wantValue:  "Bearer sk-openai-test",
		},
		{
			name: "cohere_bearer",
			setupEmbed: func(srvURL string) (ai.EmbeddingAPIClient, ai.ProviderEndpoint, ai.EmbeddingModel) {
				c := cohere.NewEmbeddingClient(cohere.ClientConfig{})
				ep := cohereEndpoint(srvURL, "co-test-key")
				return c, ep, ai.EmbeddingModel{ID: "m", MaxBatchSize: 10}
			},
			wantHeader: "Authorization",
			wantValue:  "Bearer co-test-key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := stubserver.NewJSONServer(fixture)
			defer srv.Close()

			client, ep, model := tc.setupEmbed(srv.URL)
			_, err := client.Embed(context.Background(), ep, model, ai.EmbeddingRequest{
				Texts:    []string{"test"},
				TaskType: ai.EmbeddingTaskDocument,
			})
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}

			reqs := srv.Requests()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(reqs))
			}
			if got := reqs[0].Header.Get(tc.wantHeader); got != tc.wantValue {
				t.Fatalf("%s: got %q, want %q", tc.wantHeader, got, tc.wantValue)
			}
		})
	}
}
