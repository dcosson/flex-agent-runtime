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

	"h2-agent-runtime/internal/ai"
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
	ai.EmbeddingTaskUnspecified:    "search_document",  // required, default
}

// Config controls Cohere provider construction.
type Config struct {
	HTTPClient *http.Client
	BaseURL    string
	APIKey     string
}

// EmbeddingProvider implements ai.EmbeddingProvider for the Cohere Embed API.
type EmbeddingProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

var _ ai.EmbeddingProvider = (*EmbeddingProvider)(nil)

// NewEmbedding constructs a Cohere embedding provider with sane defaults.
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

// API returns the provider API identifier.
func (p *EmbeddingProvider) API() string {
	return embeddingAPIName
}

// RegisterEmbedding constructs and registers the Cohere embedding provider.
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

	endpoint := strings.TrimRight(p.resolveBaseURL(model), "/") + "/embed"
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
		out := make([]ai.Embedding, len(resp.Embeddings.Int8))
		for i, vec := range resp.Embeddings.Int8 {
			vals := make([]float32, len(vec))
			for j, v := range vec {
				vals[j] = float32(v)
			}
			raw := make([]int8, len(vec))
			copy(raw, vec)
			out[i] = ai.Embedding{Index: i, Values: vals, Raw: raw}
		}
		return out

	case ai.EmbeddingEncodingUint8:
		out := make([]ai.Embedding, len(resp.Embeddings.Uint8))
		for i, vec := range resp.Embeddings.Uint8 {
			vals := make([]float32, len(vec))
			for j, v := range vec {
				vals[j] = float32(v)
			}
			raw := make([]uint8, len(vec))
			copy(raw, vec)
			out[i] = ai.Embedding{Index: i, Values: vals, Raw: raw}
		}
		return out

	case ai.EmbeddingEncodingBinary:
		out := make([]ai.Embedding, len(resp.Embeddings.Binary))
		for i, vec := range resp.Embeddings.Binary {
			vals := make([]float32, len(vec))
			for j, v := range vec {
				vals[j] = float32(v)
			}
			raw := make([]int8, len(vec))
			copy(raw, vec)
			out[i] = ai.Embedding{Index: i, Values: vals, Raw: raw}
		}
		return out

	case ai.EmbeddingEncodingUBinary:
		out := make([]ai.Embedding, len(resp.Embeddings.Ubinary))
		for i, vec := range resp.Embeddings.Ubinary {
			vals := make([]float32, len(vec))
			for j, v := range vec {
				vals[j] = float32(v)
			}
			raw := make([]uint8, len(vec))
			copy(raw, vec)
			out[i] = ai.Embedding{Index: i, Values: vals, Raw: raw}
		}
		return out

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

func resolveInputType(taskType ai.EmbeddingTaskType) string {
	if t, ok := cohereInputTypes[taskType]; ok {
		return t
	}
	return "search_document"
}

func (p *EmbeddingProvider) resolveBaseURL(model ai.EmbeddingModel) string {
	if b := strings.TrimSpace(model.BaseURL); b != "" {
		return b
	}
	return p.baseURL
}
