package openai

import "h2-agent-runtime/internal/ai"

// boolVal safely dereferences a *bool, returning false for nil.
func boolVal(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// systemRole returns "developer" or "system" depending on model compatibility.
func systemRole(model ai.Model) string {
	if model.Compat != nil && boolVal(model.Compat.SupportsDeveloperRole) {
		return "developer"
	}
	return "system"
}

// shouldIncludeStore returns whether to set "store: true" in the request.
func shouldIncludeStore(model ai.Model) bool {
	return model.Compat != nil && boolVal(model.Compat.SupportsStore)
}

// shouldRequestStreamUsage returns whether to include stream_options.include_usage.
func shouldRequestStreamUsage(model ai.Model) bool {
	if model.Compat == nil {
		return true
	}
	if model.Compat.SupportsUsageInStreaming != nil {
		return *model.Compat.SupportsUsageInStreaming
	}
	return true
}

// useMaxCompletionTokens returns true if the model uses "max_completion_tokens"
// rather than "max_tokens".
func useMaxCompletionTokens(model ai.Model) bool {
	if model.Compat != nil && model.Compat.MaxTokensField != "" {
		return model.Compat.MaxTokensField == "max_completion_tokens"
	}
	return true // default for modern OpenAI
}

// supportsStrictMode returns whether tool functions should include strict: true.
func supportsStrictMode(model ai.Model) bool {
	return model.Compat != nil && boolVal(model.Compat.SupportsStrictMode)
}

// requiresToolResultName returns whether tool result messages need the tool name.
func requiresToolResultName(model ai.Model) bool {
	return model.Compat != nil && boolVal(model.Compat.RequiresToolResultName)
}

// requiresAssistantAfterToolResult returns whether to inject synthetic assistant
// messages between tool results.
func requiresAssistantAfterToolResult(model ai.Model) bool {
	return model.Compat != nil && boolVal(model.Compat.RequiresAssistantAfterToolResult)
}

// requiresThinkingAsText returns whether thinking blocks should be converted to text.
func requiresThinkingAsText(model ai.Model) bool {
	return model.Compat != nil && boolVal(model.Compat.RequiresThinkingAsText)
}
