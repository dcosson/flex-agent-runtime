package ai

import "context"

// EmbeddingTaskType hints how embedding vectors will be used.
type EmbeddingTaskType string

const (
	EmbeddingTaskQuery          EmbeddingTaskType = "query"
	EmbeddingTaskDocument       EmbeddingTaskType = "document"
	EmbeddingTaskClassification EmbeddingTaskType = "classification"
	EmbeddingTaskClustering     EmbeddingTaskType = "clustering"
	EmbeddingTaskSimilarity     EmbeddingTaskType = "similarity"
	EmbeddingTaskUnspecified    EmbeddingTaskType = ""
)

// EmbeddingEncoding specifies embedding output representation.
type EmbeddingEncoding string

const (
	EmbeddingEncodingFloat   EmbeddingEncoding = "float"
	EmbeddingEncodingBase64  EmbeddingEncoding = "base64"
	EmbeddingEncodingInt8    EmbeddingEncoding = "int8"
	EmbeddingEncodingUint8   EmbeddingEncoding = "uint8"
	EmbeddingEncodingBinary  EmbeddingEncoding = "binary"
	EmbeddingEncodingUBinary EmbeddingEncoding = "ubinary"
)

// EmbeddingRequest is the provider-agnostic embedding request.
type EmbeddingRequest struct {
	Texts      []string
	TaskType   EmbeddingTaskType
	Dimensions int
	Encoding   EmbeddingEncoding
	OnProgress func(completed, total int)
}

// EmbeddingResponse is the provider-agnostic embedding response.
type EmbeddingResponse struct {
	Embeddings []Embedding
	Model      string
	Usage      EmbeddingUsage
}

// Embedding is one embedding vector for an input index.
type Embedding struct {
	Index  int
	Values []float32
	Raw    any
}

// EmbeddingUsage is embedding usage/cost telemetry.
type EmbeddingUsage struct {
	Tokens int
	Cost   float64
}

// EmbeddingModel describes one embedding model entry in catalog/registry.
type EmbeddingModel struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	API              string              `json:"api"`
	Provider         string              `json:"provider"`
	MaxInputTokens   int                 `json:"maxInputTokens"`
	DefaultDims      int                 `json:"defaultDims"`
	MaxDims          int                 `json:"maxDims,omitempty"`
	MinDims          int                 `json:"minDims,omitempty"`
	MaxBatchSize     int                 `json:"maxBatchSize"`
	SupportsDimCtrl  bool                `json:"supportsDimCtrl"`
	SupportsTaskType bool                `json:"supportsTaskType"`
	Headers          map[string][]string `json:"headers,omitempty"`
	Cost             EmbeddingCost       `json:"cost"`
	PricingKnown     bool                `json:"pricingKnown"`
}

type EmbeddingCost struct {
	PerMTok float64 `json:"perMTok"`
}

// EmbedFunc performs one provider-specific single-batch embedding call.
type EmbedFunc func(ctx context.Context, endpoint ProviderEndpoint, model EmbeddingModel, req EmbeddingRequest) (*EmbeddingResponse, error)
