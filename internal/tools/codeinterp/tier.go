package codeinterp

import (
	"strings"

	"go.starlark.net/syntax"
)

func ClassifyTier(code string, req ExecuteRequest) Tier {
	if strings.EqualFold(req.Tier, "full") {
		return TierFull
	}
	if strings.EqualFold(req.Tier, "lightweight") {
		return TierLightweight
	}
	if req.DatastoreType != "" && !strings.EqualFold(req.DatastoreType, "memory") {
		return TierFull
	}
	if req.MaxSteps > DefaultLightweightConfig().MaxSteps {
		return TierFull
	}
	if containsRLMCalls(code) {
		return TierFull
	}
	return TierLightweight
}

func containsRLMCalls(code string) bool {
	file, err := syntax.Parse("script.star", code, 0)
	if err != nil {
		return strings.Contains(code, "llm_call(") || strings.Contains(code, "llm_batch(")
	}
	found := false
	syntax.Walk(file, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok {
			if id, ok := call.Fn.(*syntax.Ident); ok {
				if id.Name == "llm_call" || id.Name == "llm_batch" {
					found = true
					return false
				}
			}
		}
		return !found
	})
	return found
}
