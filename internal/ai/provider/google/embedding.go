package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"h2-agent-runtime/internal/ai"
)

const embeddingAPIName = "google-embeddings"

// googleTaskTypes maps normalized task types to Google's enum values.
var googleTaskTypes = map[ai.EmbeddingTaskType]string{
	ai.EmbeddingTaskQuery:          "RETRIEVAL_QUERY",
	ai.EmbeddingTaskDocument:       "RETRIEVAL_DOCUMENT",
	ai.EmbeddingTaskClassification: "CLASSIFICATION",
	ai.EmbeddingTaskClustering:     "CLUSTERING",
	ai.EmbeddingTaskSimilarity:     "SEMANTIC_SIMILARITY",
}

// EmbeddingProvider implements ai.EmbeddingProvider for Google Gemini REST API.
type EmbeddingProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	version string
}

var _ ai.EmbeddingProvider = (*EmbeddingProvider)(nil)

// NewEmbedding constructs a Google embedding provider with sane defaults.
func NewEmbedding(cfg Config) *EmbeddingProvider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = defaultVersion
	}
	return &EmbeddingProvider{
		client:  client,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  cfg.APIKey,
		version: version,
	}
}

// API returns the provider API identifier.
func (p *EmbeddingProvider) API() string {
	return embeddingAPIName
}

// RegisterEmbedding constructs and registers the Google embedding provider.
func RegisterEmbedding(cfg Config, sourceID string) *EmbeddingProvider {
	ep := NewEmbedding(cfg)
	ai.RegisterEmbeddingProvider(ep, sourceID)
	return ep
}

// Embed dispatches embedding requests with shared batch-splitting behavior.
func (p *EmbeddingProvider) Embed(ctx context.Context, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	return ai.BatchEmbed(ctx, p.embedSingle, model, req)
}

func (p *EmbeddingProvider) embedSingle(ctx context.Context, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	// Build per-text embedding requests for batchEmbedContents
	requests := make([]embedContentRequest, len(req.Texts))
	for i, text := range req.Texts {
		r := embedContentRequest{
			Model: "models/" + model.ID,
			Content: contentObj{
				Parts: []part{{Text: text}},
			},
		}
		if tt, ok := googleTaskTypes[req.TaskType]; ok && tt != "" {
			r.TaskType = tt
		}
		if req.Dimensions > 0 {
			r.OutputDimensionality = &req.Dimensions
		}
		requests[i] = r
	}

	wireReq := batchEmbedContentsRequest{Requests: requests}
	payload, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	base := p.resolveBaseURL(model)
	endpoint := fmt.Sprintf("%s/%s/models/%s:batchEmbedContents", strings.TrimRight(base, "/"), p.version, model.ID)
	if p.apiKey != "" {
		endpoint += "?key=" + p.apiKey
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")

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

	var wireResp batchEmbedContentsResponse
	if err := json.Unmarshal(body, &wireResp); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	out := make([]ai.Embedding, len(wireResp.Embeddings))
	for i, emb := range wireResp.Embeddings {
		out[i] = ai.Embedding{
			Index:  i,
			Values: append([]float32(nil), emb.Values...),
		}
	}

	return &ai.EmbeddingResponse{
		Embeddings: out,
		Model:      model.ID,
		Usage:      ai.EmbeddingUsage{}, // Google doesn't report usage
	}, nil
}

func (p *EmbeddingProvider) resolveBaseURL(model ai.EmbeddingModel) string {
	if b := strings.TrimSpace(model.BaseURL); b != "" {
		return b
	}
	return p.baseURL
}
