package codeinterp

import (
	"fmt"
	"strings"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/agent"
	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/tools/codeinterp/datastore"
	"go.starlark.net/starlark"
)

func (s *executionState) builtinDiscover(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var keyword string
	if err := starlark.UnpackArgs("discover", args, kwargs, "keyword", &keyword); err != nil {
		return nil, err
	}
	keyword = strings.ToLower(keyword)
	out := make([]any, 0)
	for _, t := range s.runtime.catalog {
		if keyword == "" || strings.Contains(strings.ToLower(t.Name), keyword) || strings.Contains(strings.ToLower(t.Description), keyword) {
			out = append(out, map[string]any{"name": t.Name, "description": t.Description})
		}
	}
	s.incDiscoverCalls()
	s.addTrace(TraceStep{Kind: "builtin", Name: "discover", Input: map[string]any{"keyword": keyword}, Output: out})
	return toStarlark(out)
}

func (s *executionState) builtinDescribe(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var name string
	if err := starlark.UnpackArgs("describe", args, kwargs, "tool_name", &name); err != nil {
		return nil, err
	}
	for _, t := range s.runtime.catalog {
		if t.Name == name {
			resp := map[string]any{"name": t.Name, "description": t.Description, "parameters": string(t.Parameters)}
			s.incDescribeCalls()
			s.addTrace(TraceStep{Kind: "builtin", Name: "describe", Input: map[string]any{"tool_name": name}, Output: resp})
			return toStarlark(resp)
		}
	}
	return nil, fmt.Errorf("tool not found: %s", name)
}

func (s *executionState) builtinInvoke(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var name string
	var paramsV starlark.Value
	if err := starlark.UnpackArgs("invoke", args, kwargs, "tool_name", &name, "params", &paramsV); err != nil {
		return nil, err
	}
	paramsAny, err := fromStarlark(paramsV)
	if err != nil {
		return nil, err
	}
	params, _ := paramsAny.(map[string]any)

	for _, t := range s.runtime.catalog {
		if t.Name == name {
			start := time.Now()
			res, execErr := t.Execute(s.ctx, s.nextToolCallID(), params, func(update agent.AgentToolResult) {
				s.addTrace(TraceStep{Kind: "tool_update", Name: name, Output: normalizeToolResult(update)})
			})
			s.incToolCalls()
			out := normalizeToolResult(res)
			step := TraceStep{Kind: "tool", Name: name, Input: params, Output: out, DurationMs: time.Since(start).Milliseconds()}
			if execErr != nil {
				step.Error = execErr.Error()
			}
			s.addTrace(step)
			if execErr != nil {
				return nil, execErr
			}
			return toStarlark(out)
		}
	}
	return nil, fmt.Errorf("tool not found: %s", name)
}

func (s *executionState) builtinLog(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var message string
	if err := starlark.UnpackArgs("log", args, kwargs, "message", &message); err != nil {
		return nil, err
	}
	s.addTrace(TraceStep{Kind: "log", Name: "log", Output: message})
	return starlark.None, nil
}

func (s *executionState) builtinLLMCall(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	return s.runLLMCall(args, kwargs)
}

func (s *executionState) runLLMCall(args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var prompt, contextText, modelOverride string
	var maxTokens int
	if err := starlark.UnpackArgs("llm_call", args, kwargs, "prompt", &prompt, "context?", &contextText, "model?", &modelOverride, "max_tokens?", &maxTokens); err != nil {
		return nil, err
	}
	if s.cfg.MaxLLMCalls == 0 {
		return nil, fmt.Errorf("llm_call disabled for this tier")
	}
	model, err := s.resolveModel(modelOverride)
	if err != nil {
		return nil, err
	}
	estTokens := maxTokens
	if estTokens <= 0 {
		estTokens = 1024
	}
	estCost := float64(estTokens) * (model.Cost.Output / 1_000_000)
	if err := s.checkLLMBudget(1, estTokens, estCost); err != nil {
		return nil, err
	}
	llmCtx := ai.Context{SystemPrompt: contextText, Messages: []ai.Message{&ai.UserMessage{Content: []ai.ContentBlock{&ai.TextContent{Text: prompt}}, Timestamp: ai.TimeToMillis(time.Now())}}}
	opts := ai.SimpleStreamOptions{}
	if maxTokens > 0 {
		opts.MaxTokens = &maxTokens
	}
	msg, err := ai.CompleteSimple(s.ctx, model, llmCtx, opts)
	if err != nil {
		s.addTrace(TraceStep{Kind: "llm", Name: "llm_call", Input: map[string]any{"model": model.ID}, Error: err.Error()})
		return nil, err
	}
	s.recordLLMUsage(msg)

	respText := ""
	for _, b := range msg.Content {
		if t, ok := b.(*ai.TextContent); ok {
			if respText != "" {
				respText += "\n"
			}
			respText += t.Text
		}
	}
	resp := map[string]any{"response": respText, "usage": usageMap(msg), "model": model.ID}
	s.addTrace(TraceStep{Kind: "llm", Name: "llm_call", Input: map[string]any{"model": model.ID}, Output: resp})
	return toStarlark(resp)
}

func (s *executionState) builtinLLMBatch(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var callsV starlark.Value
	if err := starlark.UnpackArgs("llm_batch", args, kwargs, "calls", &callsV); err != nil {
		return nil, err
	}
	callsAny, err := fromStarlark(callsV)
	if err != nil {
		return nil, err
	}
	arr, ok := callsAny.([]any)
	if !ok {
		return nil, fmt.Errorf("calls must be a list")
	}
	if s.cfg.MaxLLMCalls == 0 {
		return nil, fmt.Errorf("llm_batch disabled for this tier")
	}
	if err := s.checkLLMBudget(len(arr), 1, 0); err != nil {
		return nil, err
	}

	jobs := make([]func() (map[string]any, error), 0, len(arr))
	for _, c := range arr {
		callMap, _ := c.(map[string]any)
		prompt, _ := callMap["prompt"].(string)
		ctxText, _ := callMap["context"].(string)
		model, _ := callMap["model"].(string)
		maxTokens, _ := callMap["max_tokens"].(int64)
		jobs = append(jobs, func() (map[string]any, error) {
			v, err := s.runLLMCall(starlark.Tuple{starlark.String(prompt)}, []starlark.Tuple{
				{starlark.String("context"), starlark.String(ctxText)},
				{starlark.String("model"), starlark.String(model)},
				{starlark.String("max_tokens"), starlark.MakeInt64(maxTokens)},
			})
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			gv, err := fromStarlark(v)
			if err != nil {
				return map[string]any{"error": err.Error()}, nil
			}
			m, _ := gv.(map[string]any)
			return m, nil
		})
	}
	results, err := runBatch(s.ctx, s.cfg.LLMConcurrency, jobs)
	if err != nil {
		return nil, err
	}
	out := make([]any, len(results))
	for i := range results {
		out[i] = results[i]
	}
	s.addTrace(TraceStep{Kind: "llm", Name: "llm_batch", Input: map[string]any{"count": len(arr)}, Output: out})
	return toStarlark(out)
}

func (s *executionState) builtinStoreWrite(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var key, data string
	if err := starlark.UnpackArgs("store_write", args, kwargs, "key", &key, "data", &data); err != nil {
		return nil, err
	}
	if err := s.store.Write(key, []byte(data)); err != nil {
		return nil, err
	}
	s.incStoreOps()
	s.addTrace(TraceStep{Kind: "store", Name: "store_write", Input: map[string]any{"key": key, "size": len(data)}})
	return starlark.None, nil
}

func (s *executionState) builtinStoreRead(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var key string
	if err := starlark.UnpackArgs("store_read", args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	buf, err := s.store.Read(key)
	if err != nil {
		return nil, err
	}
	s.incStoreOps()
	s.addTrace(TraceStep{Kind: "store", Name: "store_read", Input: map[string]any{"key": key}, Output: map[string]any{"size": len(buf)}})
	return starlark.String(string(buf)), nil
}

func (s *executionState) builtinStoreReadRange(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var key string
	var offset, limit int64
	if err := starlark.UnpackArgs("store_read_range", args, kwargs, "key", &key, "offset", &offset, "limit", &limit); err != nil {
		return nil, err
	}
	buf, err := s.store.ReadRange(key, offset, limit)
	if err != nil {
		return nil, err
	}
	s.incStoreOps()
	s.addTrace(TraceStep{Kind: "store", Name: "store_read_range", Input: map[string]any{"key": key, "offset": offset, "limit": limit}, Output: map[string]any{"size": len(buf)}})
	return starlark.String(string(buf)), nil
}

func (s *executionState) builtinStoreSearch(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var keyPrefix, pattern string
	if err := starlark.UnpackArgs("store_search", args, kwargs, "key", &keyPrefix, "pattern", &pattern); err != nil {
		return nil, err
	}
	searcher, ok := s.store.(datastore.SearchableDataStore)
	if !ok {
		return nil, datastore.ErrNotSupported
	}
	matches, err := searcher.Search(keyPrefix, pattern)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(matches))
	for _, m := range matches {
		out = append(out, map[string]any{"key": m.Key, "line": m.Line, "column": m.Column, "content": m.Content})
	}
	s.incStoreOps()
	s.addTrace(TraceStep{Kind: "store", Name: "store_search", Input: map[string]any{"prefix": keyPrefix, "pattern": pattern}, Output: out})
	return toStarlark(out)
}

func (s *executionState) builtinStoreList(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var prefix string
	if err := starlark.UnpackArgs("store_list", args, kwargs, "prefix", &prefix); err != nil {
		return nil, err
	}
	keys, err := s.store.List(prefix)
	if err != nil {
		return nil, err
	}
	vals := make([]any, len(keys))
	for i := range keys {
		vals[i] = keys[i]
	}
	s.incStoreOps()
	s.addTrace(TraceStep{Kind: "store", Name: "store_list", Input: map[string]any{"prefix": prefix}, Output: vals})
	return toStarlark(vals)
}

func (s *executionState) builtinStoreDelete(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := s.step(1); err != nil {
		return nil, err
	}
	var key string
	if err := starlark.UnpackArgs("store_delete", args, kwargs, "key", &key); err != nil {
		return nil, err
	}
	if err := s.store.Delete(key); err != nil {
		return nil, err
	}
	s.incStoreOps()
	s.addTrace(TraceStep{Kind: "store", Name: "store_delete", Input: map[string]any{"key": key}})
	return starlark.None, nil
}
