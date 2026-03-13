package codeinterp

type ExecuteRequest struct {
	Code          string
	Entrypoint    string
	Args          map[string]any
	Tier          string
	MaxSteps      int
	TimeoutMs     int
	MaxLLMTokens  int
	MaxLLMCostUSD float64
	DatastoreType string
}

type TraceStep struct {
	Kind       string         `json:"kind"`
	Name       string         `json:"name"`
	Input      map[string]any `json:"input,omitempty"`
	Output     any            `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	DurationMs int64          `json:"duration_ms,omitempty"`
}

type Stats struct {
	Steps         int     `json:"steps"`
	DurationMs    int64   `json:"duration_ms"`
	ToolCalls     int     `json:"tool_calls"`
	DiscoverCalls int     `json:"discover_calls"`
	DescribeCalls int     `json:"describe_calls"`
	LLMCalls      int     `json:"llm_calls"`
	LLMTokensUsed int     `json:"llm_tokens_used"`
	LLMCostUSD    float64 `json:"llm_cost_usd"`
	StoreOps      int     `json:"store_ops"`
}

type ExecuteResult struct {
	Result    any         `json:"result"`
	Trace     []TraceStep `json:"trace"`
	Stats     Stats       `json:"stats"`
	Truncated bool        `json:"truncated"`
	Tier      string      `json:"tier"`
}
