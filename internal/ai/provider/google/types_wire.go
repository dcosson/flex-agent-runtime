package google

import "encoding/json"

// --- Request types (outbound) ---

type generateContentRequest struct {
	Contents          []contentObj      `json:"contents"`
	SystemInstruction *contentObj       `json:"systemInstruction,omitempty"`
	Tools             []toolObj         `json:"tools,omitempty"`
	ToolConfig        *toolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
	SafetySettings    []safetySetting   `json:"safetySettings,omitempty"`
}

type contentObj struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

// part is a union — exactly one field is populated per part.
type part struct {
	Text             string            `json:"text,omitempty"`
	InlineData       *inlineData       `json:"inlineData,omitempty"`
	FunctionCall     *functionCallWire `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
	Thought          *bool             `json:"thought,omitempty"`
	ThoughtSignature []byte            `json:"thoughtSignature,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCallWire struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id,omitempty"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
	ID       string         `json:"id,omitempty"`
}

type toolObj struct {
	FunctionDeclarations []functionDeclaration `json:"functionDeclarations,omitempty"`
}

type functionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type toolConfig struct {
	FunctionCallingConfig *functionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type functionCallingConfig struct {
	Mode string `json:"mode"`
}

type generationConfig struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	IncludeThoughts *bool  `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

type safetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// --- Response types (inbound) ---

type generateContentResponse struct {
	Candidates     []candidate     `json:"candidates"`
	UsageMetadata  *usageMetadata  `json:"usageMetadata,omitempty"`
	PromptFeedback *promptFeedback `json:"promptFeedback,omitempty"`
	ModelVersion   string          `json:"modelVersion,omitempty"`
}

type candidate struct {
	Content       *contentObj    `json:"content,omitempty"`
	FinishReason  string         `json:"finishReason,omitempty"`
	SafetyRatings []safetyRating `json:"safetyRatings,omitempty"`
	Index         int            `json:"index"`
}

type safetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
	Blocked     bool   `json:"blocked"`
}

type promptFeedback struct {
	BlockReason   string         `json:"blockReason,omitempty"`
	SafetyRatings []safetyRating `json:"safetyRatings,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
}

// --- Error response ---

type googleErrorResponse struct {
	Error googleErrorBody `json:"error"`
}

type googleErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
