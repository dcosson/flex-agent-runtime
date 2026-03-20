package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

const embeddingAPIName = "openai-embeddings"

// EmbeddingClient implements ai.EmbeddingAPIClient for OpenAI-compatible embedding APIs.
// It is stateless — base URL and API key come from ProviderEndpoint per-call.
type EmbeddingClient struct {
	httpClient *http.Client
}

var _ ai.EmbeddingAPIClient = (*EmbeddingClient)(nil)

// NewEmbeddingClient constructs an OpenAI embedding client.
func NewEmbeddingClient(cfg ClientConfig) *EmbeddingClient {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &EmbeddingClient{httpClient: client}
}

// ClientType returns the embedding API client type identifier.
func (c *EmbeddingClient) ClientType() string {
	return embeddingAPIName
}

// RegisterEmbeddingClient creates and registers the OpenAI embedding API client.
func RegisterEmbeddingClient(cfg ClientConfig) *EmbeddingClient {
	c := NewEmbeddingClient(cfg)
	ai.RegisterEmbeddingAPIClient(c)
	return c
}

// Embed dispatches embedding requests with shared batch-splitting behavior.
func (c *EmbeddingClient) Embed(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	return ai.BatchEmbed(ctx, c.embedSingle, endpoint, model, req)
}

func (c *EmbeddingClient) embedSingle(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	wireReq := embeddingRequestWire{
		Model: model.ID,
		Input: append([]string(nil), req.Texts...),
	}
	if req.Dimensions > 0 {
		wireReq.Dimensions = &req.Dimensions
	}
	if req.Encoding != "" {
		enc := string(req.Encoding)
		wireReq.EncodingFormat = &enc
	}

	payload, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	url := strings.TrimRight(endpoint.BaseURL, "/") + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	if endpoint.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+endpoint.APIKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, providerError(resp.StatusCode, decodeErrorMessage(body))
	}

	var wireResp embeddingResponseWire
	if err := json.Unmarshal(body, &wireResp); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	out := make([]ai.Embedding, len(wireResp.Data))
	for i, emb := range wireResp.Data {
		idx := emb.Index
		if idx < 0 {
			idx = i
		}
		out[i] = ai.Embedding{
			Index:  idx,
			Values: append([]float32(nil), emb.Embedding...),
		}
	}

	modelID := wireResp.Model
	if modelID == "" {
		modelID = model.ID
	}
	return &ai.EmbeddingResponse{
		Embeddings: out,
		Model:      modelID,
		Usage: ai.EmbeddingUsage{
			Tokens: wireResp.Usage.TotalTokens,
		},
	}, nil
}
