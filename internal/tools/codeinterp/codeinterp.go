package codeinterp

import (
	"context"
	"encoding/json"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
)

var executeScriptSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "code": {"type": "string"},
    "entrypoint": {"type": "string"},
    "args": {"type": "object"},
    "tier": {"type": "string", "enum": ["lightweight", "full"]},
    "max_steps": {"type": "integer"},
    "timeout_ms": {"type": "integer"},
    "max_llm_tokens": {"type": "integer"},
    "max_llm_cost_usd": {"type": "number"},
    "datastore_type": {"type": "string", "enum": ["memory", "fs", "blob", "sql"]}
  },
  "required": ["code"]
}`)

func NewTool(model ai.Model, catalog []agent.AgentTool) agent.AgentTool {
	rt := NewRuntime(model, catalog)
	return agent.AgentTool{
		Tool: ai.Tool{
			Name:        "execute_script",
			Description: "Execute sandboxed Starlark script with progressive tool discovery and invocation",
			Parameters:  executeScriptSchema,
		},
		Label: "execute_script",
		Execute: func(ctx context.Context, _ string, params map[string]any, _ func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
			req := ExecuteRequest{}
			if v, ok := params["code"].(string); ok {
				req.Code = v
			}
			if req.Code == "" {
				return agent.AgentToolResult{IsError: true, Content: []ai.ContentBlock{&ai.TextContent{Text: "missing required parameter: code"}}}, nil
			}
			if v, ok := params["entrypoint"].(string); ok {
				req.Entrypoint = v
			}
			if v, ok := params["args"].(map[string]any); ok {
				req.Args = v
			}
			if v, ok := params["tier"].(string); ok {
				req.Tier = v
			}
			if v, ok := params["datastore_type"].(string); ok {
				req.DatastoreType = v
			}
			if v, ok := intAny(params["max_steps"]); ok {
				req.MaxSteps = v
			}
			if v, ok := intAny(params["timeout_ms"]); ok {
				req.TimeoutMs = v
			}
			if v, ok := intAny(params["max_llm_tokens"]); ok {
				req.MaxLLMTokens = v
			}
			if v, ok := floatAny(params["max_llm_cost_usd"]); ok {
				req.MaxLLMCostUSD = v
			}

			res, err := rt.Execute(ctx, req)
			if err != nil {
				return agent.AgentToolResult{IsError: true, Content: []ai.ContentBlock{&ai.TextContent{Text: err.Error()}}}, err
			}
			b, _ := json.Marshal(res)
			return agent.AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: string(b)}}}, nil
		},
	}
}

func intAny(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	default:
		return 0, false
	}
}

func floatAny(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	default:
		return 0, false
	}
}
