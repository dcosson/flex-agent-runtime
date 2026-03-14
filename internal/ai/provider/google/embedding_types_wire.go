package google

// --- Embedding request types (outbound) ---

type batchEmbedContentsRequest struct {
	Requests []embedContentRequest `json:"requests"`
}

type embedContentRequest struct {
	Model                string     `json:"model"`
	Content              contentObj `json:"content"`
	TaskType             string     `json:"taskType,omitempty"`
	OutputDimensionality *int       `json:"outputDimensionality,omitempty"`
}

// --- Embedding response types (inbound) ---

type batchEmbedContentsResponse struct {
	Embeddings []contentEmbedding `json:"embeddings"`
}

type contentEmbedding struct {
	Values []float32 `json:"values"`
}
