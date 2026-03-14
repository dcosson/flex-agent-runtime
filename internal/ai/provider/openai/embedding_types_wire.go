package openai

type embeddingRequestWire struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     *int     `json:"dimensions,omitempty"`
	EncodingFormat *string  `json:"encoding_format,omitempty"`
}

type embeddingResponseWire struct {
	Object string                `json:"object"`
	Data   []embeddingVectorWire `json:"data"`
	Model  string                `json:"model"`
	Usage  embeddingUsageWire    `json:"usage"`
}

type embeddingVectorWire struct {
	Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type embeddingUsageWire struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}
