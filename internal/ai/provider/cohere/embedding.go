package cohere

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

const (
	embeddingAPIName = "cohere-embeddings"
	defaultBaseURL   = "https://api.cohere.com/v2"
	defaultTimeout   = 60 * time.Second
)

// cohereInputTypes maps normalized task types to Cohere's input_type values.
var cohereInputTypes = map[ai.EmbeddingTaskType]string{
	ai.EmbeddingTaskQuery:          "search_query",
	ai.EmbeddingTaskDocument:       "search_document",
	ai.EmbeddingTaskClassification: "classification",
	ai.EmbeddingTaskClustering:     "clustering",
	ai.EmbeddingTaskSimilarity:     "search_document", // closest match
	ai.EmbeddingTaskUnspecified:    "search_document", // required, default
}

// ClientConfig controls Cohere client construction.
type ClientConfig struct {
	HTTPClient *http.Client
}

// EmbeddingClient implements ai.EmbeddingAPIClient for the Cohere Embed API.
// It is stateless — base URL and API key come from ProviderEndpoint per-call.
type EmbeddingClient struct {
	httpClient *http.Client
}

var _ ai.EmbeddingAPIClient = (*EmbeddingClient)(nil)

// NewEmbeddingClient constructs a Cohere embedding client.
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

// RegisterEmbeddingClient creates and registers the Cohere embedding API client.
func RegisterEmbeddingClient(cfg ClientConfig) *EmbeddingClient {
	c := NewEmbeddingClient(cfg)
	ai.RegisterEmbeddingAPIClient(c)
	return c
}

// Embed dispatches embedding requests with shared batch-splitting behavior.
// If endpoint.APIKey is empty, resolves from provider config env vars.
func (c *EmbeddingClient) Embed(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	endpoint = ai.ResolveEmbeddingEndpoint(endpoint)
	return ai.BatchEmbed(ctx, c.embedSingle, endpoint, model, req)
}

func (c *EmbeddingClient) embedSingle(ctx context.Context, endpoint ai.ProviderEndpoint, model ai.EmbeddingModel, req ai.EmbeddingRequest) (*ai.EmbeddingResponse, error) {
	wireReq := embedRequestWire{
		Model:     model.ID,
		Texts:     append([]string(nil), req.Texts...),
		InputType: resolveInputType(req.TaskType),
	}
	if req.Dimensions > 0 {
		wireReq.OutputDimension = &req.Dimensions
	}
	if req.Encoding != "" && req.Encoding != ai.EmbeddingEncodingFloat {
		wireReq.EmbeddingTypes = []string{string(req.Encoding)}
	}

	payload, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	url := strings.TrimRight(endpoint.BaseURL, "/") + "/embed"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
	}
	httpReq.Header.Set("content-type", "application/json")
	if endpoint.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+endpoint.APIKey)
	}
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

	var wireResp embedResponseWire
	if err := json.Unmarshal(body, &wireResp); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}

	out := extractEmbeddings(wireResp, req.Encoding)

	return &ai.EmbeddingResponse{
		Embeddings: out,
		Model:      model.ID,
		Usage: ai.EmbeddingUsage{
			Tokens: wireResp.Meta.BilledUnits.InputTokens,
		},
	}, nil
}

// extractEmbeddings converts the type-keyed Cohere response into normalized embeddings.
func extractEmbeddings(resp embedResponseWire, encoding ai.EmbeddingEncoding) []ai.Embedding {
	switch encoding {
	case ai.EmbeddingEncodingInt8:
		return quantizedToEmbeddings(resp.Embeddings.Int8)
	case ai.EmbeddingEncodingBinary:
		return quantizedToEmbeddings(resp.Embeddings.Binary)
	case ai.EmbeddingEncodingUint8:
		return quantizedToEmbeddings(resp.Embeddings.Uint8)
	case ai.EmbeddingEncodingUBinary:
		return quantizedToEmbeddings(resp.Embeddings.Ubinary)
	default: // float (default)
		out := make([]ai.Embedding, len(resp.Embeddings.Float))
		for i, vec := range resp.Embeddings.Float {
			out[i] = ai.Embedding{
				Index:  i,
				Values: append([]float32(nil), vec...),
			}
		}
		return out
	}
}

// quantizedToEmbeddings converts quantized integer vectors to embeddings with both
// float32 Values (for uniform access) and Raw (for quantized consumers).
func quantizedToEmbeddings[T int8 | uint8](vecs [][]T) []ai.Embedding {
	out := make([]ai.Embedding, len(vecs))
	for i, vec := range vecs {
		vals := make([]float32, len(vec))
		for j, v := range vec {
			vals[j] = float32(v)
		}
		raw := make([]T, len(vec))
		copy(raw, vec)
		out[i] = ai.Embedding{Index: i, Values: vals, Raw: raw}
	}
	return out
}

func resolveInputType(taskType ai.EmbeddingTaskType) string {
	if t, ok := cohereInputTypes[taskType]; ok {
		return t
	}
	return "search_document"
}
