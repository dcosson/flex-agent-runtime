package google

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

const embeddingAPIName = "google-embeddings"

// googleTaskTypes maps normalized task types to Google's enum values.
var googleTaskTypes = map[ai.EmbeddingTaskType]string{
	ai.EmbeddingTaskQuery:          "RETRIEVAL_QUERY",
	ai.EmbeddingTaskDocument:       "RETRIEVAL_DOCUMENT",
	ai.EmbeddingTaskClassification: "CLASSIFICATION",
	ai.EmbeddingTaskClustering:     "CLUSTERING",
	ai.EmbeddingTaskSimilarity:     "SEMANTIC_SIMILARITY",
}

// EmbeddingClient implements ai.EmbeddingAPIClient for the Google Gemini REST API.
// It is stateless — base URL, API key, and version come from ProviderEndpoint per-call.
type EmbeddingClient struct {
	httpClient *http.Client
}

var _ ai.EmbeddingAPIClient = (*EmbeddingClient)(nil)

// NewEmbeddingClient constructs a Google embedding client.
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

// RegisterEmbeddingClient creates and registers the Google embedding API client.
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
	// Build per-text embedding requests for batchEmbedContents
	requests := make([]embedContentRequest, len(req.Texts))
	for i, text := range req.Texts {
		r := embedContentRequest{
			Model: "models/" + model.ID,
			Content: contentObj{
				Parts: []part{{Text: text}},
			},
		}
		// Unspecified maps to "" in googleTaskTypes; omit taskType from request via omitempty.
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

	version := endpoint.ProviderSpecific["apiVersion"]
	if version == "" {
		version = defaultVersion
	}
	baseURL := strings.TrimRight(endpoint.BaseURL, "/")
	url := fmt.Sprintf("%s/%s/models/%s:batchEmbedContents", baseURL, version, model.ID)
	if endpoint.APIKey != "" {
		url += "?key=" + endpoint.APIKey
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	for k, vs := range endpoint.Headers {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, vs := range model.Headers {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
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

	// Google doesn't report usage — estimate tokens from input text (~4 chars/token).
	var estimatedTokens int
	for _, text := range req.Texts {
		estimatedTokens += (len(text) + 3) / 4
	}

	return &ai.EmbeddingResponse{
		Embeddings: out,
		Model:      model.ID,
		Usage:      ai.EmbeddingUsage{Tokens: estimatedTokens},
	}, nil
}
