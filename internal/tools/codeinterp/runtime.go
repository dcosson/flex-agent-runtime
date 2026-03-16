package codeinterp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	"flex-agent-runtime/internal/agent"
	"flex-agent-runtime/internal/ai"
	"flex-agent-runtime/internal/tools/codeinterp/datastore"
)

type Runtime struct {
	defaultModel ai.Model
	catalog      []agent.AgentTool
}

func NewRuntime(model ai.Model, catalog []agent.AgentTool) *Runtime {
	cp := make([]agent.AgentTool, len(catalog))
	copy(cp, catalog)
	return &Runtime{defaultModel: model, catalog: cp}
}

type executionState struct {
	ctx           context.Context
	cfg           Config
	req           ExecuteRequest
	runtime       *Runtime
	store         datastore.DataStore
	trace         *traceCollector
	mu            sync.Mutex
	steps         int
	started       time.Time
	stats         Stats
	llmTokensUsed int
	llmCost       float64
}

func (s *executionState) step(n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		n = 1
	}
	s.steps += n
	s.stats.Steps += n
	if s.cfg.MaxSteps > 0 && s.steps > s.cfg.MaxSteps {
		return BudgetExceededError{Msg: fmt.Sprintf("max_steps exceeded (%d)", s.cfg.MaxSteps)}
	}
	if s.cfg.MaxWallTime > 0 && time.Since(s.started) > s.cfg.MaxWallTime {
		return TimeoutError{Msg: fmt.Sprintf("wall time exceeded (%s)", s.cfg.MaxWallTime)}
	}
	select {
	case <-s.ctx.Done():
		return TimeoutError{Msg: s.ctx.Err().Error()}
	default:
	}
	return nil
}

func (s *executionState) addTrace(step TraceStep) {
	s.trace.add(step)
}

func (s *executionState) incDiscoverCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.DiscoverCalls++
}

func (s *executionState) incDescribeCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.DescribeCalls++
}

func (s *executionState) nextToolCallID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("ci-%d", s.stats.ToolCalls+1)
}

func (s *executionState) incToolCalls() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.ToolCalls++
}

func (s *executionState) incStoreOps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.StoreOps++
}

func (r *Runtime) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	tier := ClassifyTier(req.Code, req)
	cfg := DefaultLightweightConfig()
	if tier == TierFull {
		cfg = DefaultFullConfig()
	}
	if req.MaxSteps > 0 {
		cfg.MaxSteps = req.MaxSteps
	}
	if req.TimeoutMs > 0 {
		cfg.MaxWallTime = time.Duration(req.TimeoutMs) * time.Millisecond
	}
	if req.MaxLLMTokens > 0 {
		cfg.MaxLLMTokens = req.MaxLLMTokens
	}
	if req.MaxLLMCostUSD > 0 {
		cfg.MaxLLMCostUSD = req.MaxLLMCostUSD
	}
	cfg = cfg.withDefaults()

	store, err := r.newDataStore(req, cfg)
	if err != nil {
		return ExecuteResult{}, err
	}
	defer store.Close()

	st := &executionState{
		ctx:     ctx,
		cfg:     cfg,
		req:     req,
		runtime: r,
		store:   store,
		trace:   newTraceCollector(cfg.MaxTraceBytes),
		started: time.Now(),
	}

	thread := &starlark.Thread{Name: "codeinterp"}
	thread.Load = func(_ *starlark.Thread, _ string) (starlark.StringDict, error) {
		return nil, fmt.Errorf("load is disabled")
	}

	globals := st.builtins()
	fileGlobals, err := starlark.ExecFileOptions(syntax.LegacyFileOptions(), thread, "script.star", req.Code, globals)
	if err != nil {
		return st.finish(nil, err, tier), nil
	}

	entry := req.Entrypoint
	if entry == "" {
		entry = "main"
	}
	fn, ok := fileGlobals[entry]
	if !ok {
		return st.finish(nil, fmt.Errorf("entrypoint %q not found", entry), tier), nil
	}
	callable, ok := fn.(starlark.Callable)
	if !ok {
		return st.finish(nil, fmt.Errorf("entrypoint %q is not callable", entry), tier), nil
	}
	argVal, err := toStarlark(req.Args)
	if err != nil {
		return st.finish(nil, err, tier), nil
	}
	ret, err := starlark.Call(thread, callable, starlark.Tuple{argVal}, nil)
	if err != nil {
		return st.finish(nil, err, tier), nil
	}
	goVal, err := fromStarlark(ret)
	if err != nil {
		return st.finish(nil, err, tier), nil
	}
	return st.finish(goVal, nil, tier), nil
}

func (s *executionState) finish(result any, execErr error, tier Tier) ExecuteResult {
	stats := s.snapshotStats()
	stats.DurationMs = time.Since(s.started).Milliseconds()
	if execErr == nil {
		if err := s.validateResultSize(result); err != nil {
			execErr = err
		}
	}
	res := ExecuteResult{
		Result:    result,
		Trace:     s.trace.snapshot(),
		Stats:     stats,
		Truncated: s.trace.isTruncated(),
		Tier:      map[Tier]string{TierLightweight: "lightweight", TierFull: "full"}[tier],
	}
	if execErr != nil {
		s.addTrace(TraceStep{Kind: "error", Name: "runtime", Error: execErr.Error()})
		res.Result = map[string]any{"error": execErr.Error()}
	}
	return res
}

func (r *Runtime) newDataStore(req ExecuteRequest, cfg Config) (datastore.DataStore, error) {
	switch req.DatastoreType {
	case "", "memory":
		return datastore.NewMemoryDataStore(cfg.MaxStoreBytes, cfg.MaxStoreKeySize, cfg.MaxStoreValueSize), nil
	case "fs":
		root, err := os.MkdirTemp("", "codeinterp-store-*")
		if err != nil {
			return nil, err
		}
		return datastore.NewFSDataStore(root, cfg.MaxStoreBytes, cfg.MaxStoreKeySize, cfg.MaxStoreValueSize)
	case "blob":
		return datastore.NewBlobDataStore(), nil
	case "sql":
		return datastore.NewSQLDataStore(), nil
	default:
		return nil, fmt.Errorf("unknown datastore_type: %s", req.DatastoreType)
	}
}

func (s *executionState) builtins() starlark.StringDict {
	return starlark.StringDict{
		"discover":         starlark.NewBuiltin("discover", s.builtinDiscover),
		"describe":         starlark.NewBuiltin("describe", s.builtinDescribe),
		"invoke":           starlark.NewBuiltin("invoke", s.builtinInvoke),
		"log":              starlark.NewBuiltin("log", s.builtinLog),
		"llm_call":         starlark.NewBuiltin("llm_call", s.builtinLLMCall),
		"llm_batch":        starlark.NewBuiltin("llm_batch", s.builtinLLMBatch),
		"store_write":      starlark.NewBuiltin("store_write", s.builtinStoreWrite),
		"store_read":       starlark.NewBuiltin("store_read", s.builtinStoreRead),
		"store_read_range": starlark.NewBuiltin("store_read_range", s.builtinStoreReadRange),
		"store_search":     starlark.NewBuiltin("store_search", s.builtinStoreSearch),
		"store_list":       starlark.NewBuiltin("store_list", s.builtinStoreList),
		"store_delete":     starlark.NewBuiltin("store_delete", s.builtinStoreDelete),
	}
}

func (s *executionState) resolveModel(modelOverride string) (ai.Model, error) {
	if modelOverride == "" && s.cfg.DefaultLLMModel != "" {
		modelOverride = s.cfg.DefaultLLMModel
	}
	if modelOverride == "" {
		if s.runtime.defaultModel.API == "" {
			return ai.Model{}, fmt.Errorf("no default model configured")
		}
		return s.runtime.defaultModel, nil
	}
	if s.runtime.defaultModel.Provider == "" {
		return ai.Model{}, fmt.Errorf("model override requires default provider context")
	}
	m, err := ai.GetModel(s.runtime.defaultModel.Provider, modelOverride)
	if err != nil {
		return ai.Model{}, err
	}
	return m, nil
}

func (s *executionState) checkLLMBudget(incrCalls int, estTokens int, estCost float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.MaxLLMCalls > 0 && s.stats.LLMCalls+incrCalls > s.cfg.MaxLLMCalls {
		return TokenBudgetExceededError{Msg: "llm call limit exceeded"}
	}
	if s.cfg.MaxLLMTokens > 0 && s.llmTokensUsed+estTokens > s.cfg.MaxLLMTokens {
		return TokenBudgetExceededError{Msg: "llm token budget exceeded"}
	}
	if s.cfg.MaxLLMCostUSD > 0 && s.llmCost+estCost > s.cfg.MaxLLMCostUSD {
		return TokenBudgetExceededError{Msg: "llm cost budget exceeded"}
	}
	return nil
}

func (s *executionState) recordLLMUsage(msg ai.AssistantMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.LLMCalls++
	s.llmTokensUsed += msg.Usage.Input + msg.Usage.Output
	s.llmCost += msg.Usage.Cost.Total
}

func (s *executionState) snapshotStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := s.stats
	stats.LLMTokensUsed = s.llmTokensUsed
	stats.LLMCostUSD = s.llmCost
	return stats
}

func usageMap(msg ai.AssistantMessage) map[string]any {
	return map[string]any{
		"input_tokens":  msg.Usage.Input,
		"output_tokens": msg.Usage.Output,
		"cost_usd":      msg.Usage.Cost.Total,
	}
}

func (s *executionState) validateResultSize(result any) error {
	if s.cfg.MaxResultBytes <= 0 {
		return nil
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(b) > s.cfg.MaxResultBytes {
		return fmt.Errorf("result exceeds max_result_bytes (%d > %d)", len(b), s.cfg.MaxResultBytes)
	}
	return nil
}

func normalizeToolResult(res agent.AgentToolResult) map[string]any {
	out := map[string]any{
		"is_error": res.IsError,
	}
	if len(res.Content) > 0 {
		blocks := make([]string, 0, len(res.Content))
		for _, b := range res.Content {
			if t, ok := b.(*ai.TextContent); ok {
				blocks = append(blocks, t.Text)
			}
		}
		out["content"] = strings.Join(blocks, "\n")
	}
	if res.SnapshotID != "" {
		out["snapshot_id"] = res.SnapshotID
	}
	if res.ExitCode != nil {
		out["exit_code"] = *res.ExitCode
	}
	return out
}

func runBatch[T any](ctx context.Context, maxPar int, calls []func() (T, error)) ([]T, error) {
	if maxPar <= 0 {
		maxPar = 1
	}
	out := make([]T, len(calls))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(calls))
	sem := make(chan struct{}, maxPar)
	for i, fn := range calls {
		i, fn := i, fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			v, err := fn()
			out[i] = v
			if err != nil {
				select {
				case errCh <- err:
					cancel()
				default:
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	if err, ok := <-errCh; ok {
		return out, err
	}
	return out, nil
}
