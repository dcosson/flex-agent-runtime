package cohere

// --- Embedding request types (outbound) ---

type embedRequestWire struct {
	Model          string   `json:"model"`
	Texts          []string `json:"texts"`
	InputType      string   `json:"input_type"`
	EmbeddingTypes []string `json:"embedding_types,omitempty"`
	// embed-v4+ only; omit for older models
	OutputDimension *int `json:"output_dimension,omitempty"`
}

// --- Embedding response types (inbound) ---

type embedResponseWire struct {
	ID         string              `json:"id"`
	Embeddings embeddingsContainer `json:"embeddings"`
	Meta       embedMeta           `json:"meta"`
}

// embeddingsContainer holds type-keyed embedding arrays.
// Only one key is populated per response, matching the requested type.
type embeddingsContainer struct {
	Float   [][]float32 `json:"float,omitempty"`
	Int8    [][]int8    `json:"int8,omitempty"`
	Uint8   [][]uint8   `json:"uint8,omitempty"`
	Binary  [][]int8    `json:"binary,omitempty"`
	Ubinary [][]uint8   `json:"ubinary,omitempty"`
}

type embedMeta struct {
	BilledUnits billedUnits `json:"billed_units"`
}

type billedUnits struct {
	InputTokens int `json:"input_tokens"`
}

// --- Error response ---

type cohereErrorResponse struct {
	Message string `json:"message"`
}
