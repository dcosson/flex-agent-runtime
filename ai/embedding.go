package ai

import internal "github.com/anthropics/flex-agent-runtime/internal/ai"

type (
	EmbeddingRequest  = internal.EmbeddingRequest
	EmbeddingResponse = internal.EmbeddingResponse
	Embedding         = internal.Embedding
	EmbeddingUsage    = internal.EmbeddingUsage
	EmbeddingTaskType = internal.EmbeddingTaskType
	EmbeddingEncoding = internal.EmbeddingEncoding
	EmbeddingProvider = internal.EmbeddingProvider
	EmbeddingModel    = internal.EmbeddingModel
	EmbeddingCost     = internal.EmbeddingCost
	EmbedFunc         = internal.EmbedFunc
)

const (
	EmbeddingTaskQuery          = internal.EmbeddingTaskQuery
	EmbeddingTaskDocument       = internal.EmbeddingTaskDocument
	EmbeddingTaskClassification = internal.EmbeddingTaskClassification
	EmbeddingTaskClustering     = internal.EmbeddingTaskClustering
	EmbeddingTaskSimilarity     = internal.EmbeddingTaskSimilarity
	EmbeddingTaskUnspecified    = internal.EmbeddingTaskUnspecified

	EmbeddingEncodingFloat   = internal.EmbeddingEncodingFloat
	EmbeddingEncodingBase64  = internal.EmbeddingEncodingBase64
	EmbeddingEncodingInt8    = internal.EmbeddingEncodingInt8
	EmbeddingEncodingUint8   = internal.EmbeddingEncodingUint8
	EmbeddingEncodingBinary  = internal.EmbeddingEncodingBinary
	EmbeddingEncodingUBinary = internal.EmbeddingEncodingUBinary
)

var (
	Embed                         = internal.Embed
	BatchEmbed                    = internal.BatchEmbed
	RegisterEmbeddingProvider     = internal.RegisterEmbeddingProvider
	GetEmbeddingProvider          = internal.GetEmbeddingProvider
	UnregisterEmbeddingProviders  = internal.UnregisterEmbeddingProviders
	ClearEmbeddingProviders       = internal.ClearEmbeddingProviders
	RegisterEmbeddingModel        = internal.RegisterEmbeddingModel
	GetEmbeddingModel             = internal.GetEmbeddingModel
	ListEmbeddingModels           = internal.ListEmbeddingModels
	ListEmbeddingModelsByProvider = internal.ListEmbeddingModelsByProvider
	ClearEmbeddingModels          = internal.ClearEmbeddingModels
)
