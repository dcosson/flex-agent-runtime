package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"flex-agent-runtime/internal/ai"
)

const embeddingAPIName = "openai-embeddings"

// EmbeddingProvider implements ai.EmbeddingProvider for OpenAI-compatible APIs.
type EmbeddingProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

var _ ai.EmbeddingProvider = (*EmbeddingProvider)(nil)

// NewEmbedding constructs an OpenAI embedding provider with sane defaults.
func NewEmbedding(cfg Config) *EmbeddingProvider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &EmbeddingProvider{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  cfg.APIKey,
	}
}

func (p *EmbeddingProvider) API() string {
	return embeddingAPIName
}

// RegisterEmbedding constructs and registers the OpenAI embedding provider.
func RegisterEmbedding(cfg Config, sourceID string) *EmbeddingProvider {
	p := NewEmbedding(cfg)
	ai.RegisterEmbeddingProvider(p, sourceID)
	return p
}

// Embed dispatches embedding requests with shared batch-splitting behavior.
func (p *EmbeddingProvider) Embed(ctx context.Context, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	return ai.BatchEmbed(ctx, p.embedSingle, model, req)
}

func (p *EmbeddingProvider) embedSingle(ctx context.Context, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
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

	endpoint := strings.TrimRight(p.resolveBaseURL(model), "/") + "/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
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

func (p *EmbeddingProvider) resolveBaseURL(model ai.EmbeddingModel) string {
	if b := strings.TrimSpace(model.BaseURL); b != "" {
		return b
	}
	return p.baseURL
}
