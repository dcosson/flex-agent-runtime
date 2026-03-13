package codeinterp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"h2-agent-runtime/internal/agent"
	"h2-agent-runtime/internal/ai"
	"h2-agent-runtime/internal/tools/codeinterp/datastore"
)

type fakeProvider struct{ api string }

func (p *fakeProvider) API() string { return p.api }
func (p *fakeProvider) Stream(context.Context, ai.Model, ai.Context, ai.StreamOptions) *ai.EventStream {
	es := ai.NewEventStream()
	go func() {
		defer es.Close()
		es.Send(ai.AssistantMessageEvent{Type: ai.EventDone, Message: &ai.AssistantMessage{
			Content:    []ai.ContentBlock{&ai.TextContent{Text: "summary"}},
			StopReason: ai.StopReasonStop,
			Usage: ai.Usage{
				Input:  10,
				Output: 20,
				Cost:   ai.UsageCost{Total: 0.001},
			},
			Timestamp: ai.TimeToMillis(time.Now()),
		}})
	}()
	return es
}
func (p *fakeProvider) StreamSimple(context.Context, ai.Model, ai.Context, ai.SimpleStreamOptions) *ai.EventStream {
	return p.Stream(context.Background(), ai.Model{}, ai.Context{}, ai.StreamOptions{})
}

func testCatalog() []agent.AgentTool {
	return []agent.AgentTool{
		{
			Tool:  ai.Tool{Name: "read_file", Description: "read a file"},
			Label: "read_file",
			Execute: func(ctx context.Context, toolCallID string, params map[string]any, onUpdate func(agent.AgentToolResult)) (agent.AgentToolResult, error) {
				return agent.AgentToolResult{Content: []ai.ContentBlock{&ai.TextContent{Text: "file-content"}}}, nil
			},
		},
	}
}

func TestClassifyTierAST(t *testing.T) {
	if got := ClassifyTier("def main(args):\n  return llm_call('x')", ExecuteRequest{}); got != TierFull {
		t.Fatalf("expected full tier for llm_call")
	}
	if got := ClassifyTier("def main(args):\n  x='llm_call('", ExecuteRequest{}); got != TierLightweight {
		t.Fatalf("string literal should not force full tier")
	}
	if got := ClassifyTier("def main(args):\n  return 1", ExecuteRequest{Tier: "full"}); got != TierFull {
		t.Fatalf("explicit full tier override failed")
	}
}

func TestMemoryDataStoreValidation(t *testing.T) {
	ds := datastore.NewMemoryDataStore(1024, 32, 512)
	if err := ds.Write("../x", []byte("bad")); err == nil {
		t.Fatalf("expected traversal key rejection")
	}
	if err := ds.Write("ok", []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf, err := ds.Read("ok")
	if err != nil || string(buf) != "hello" {
		t.Fatalf("read mismatch: %q err=%v", string(buf), err)
	}
}

func TestRuntimeDiscoverDescribeInvokeStore(t *testing.T) {
	rt := NewRuntime(ai.Model{}, testCatalog())
	req := ExecuteRequest{Code: `
def main(args):
  tools = discover("file")
  spec = describe("read_file")
  out = invoke("read_file", {"path":"x"})
  store_write("k", out["content"])
  val = store_read("k")
  return {"count": len(tools), "name": spec["name"], "val": val}
`}
	res, err := rt.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	m, ok := res.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", res.Result)
	}
	if m["name"] != "read_file" {
		t.Fatalf("describe mismatch: %+v", m)
	}
	if m["val"] != "file-content" {
		t.Fatalf("invoke/store mismatch: %+v", m)
	}
}

func TestRuntimeStepBudgetExceeded(t *testing.T) {
	rt := NewRuntime(ai.Model{}, testCatalog())
	req := ExecuteRequest{Code: `
def main(args):
  discover("f")
  discover("f")
  return 1
`, MaxSteps: 1}
	res, err := rt.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("execute should return result with embedded error: %v", err)
	}
	m, _ := res.Result.(map[string]any)
	if m == nil || m["error"] == nil {
		t.Fatalf("expected embedded budget error, got %+v", res.Result)
	}
}

func TestRuntimeLLMCallAndBatch(t *testing.T) {
	ai.ClearProviders()
	defer ai.ClearProviders()
	ai.RegisterProvider(&fakeProvider{api: "fake-api"}, "codeinterp-test")
	model := ai.Model{ID: "m1", API: "fake-api", Provider: "fake", MaxTokens: 2048}
	ai.RegisterModel(model)

	rt := NewRuntime(model, testCatalog())
	req := ExecuteRequest{Code: `
def main(args):
  one = llm_call("hello", max_tokens=10)
  batch = llm_batch([
    {"prompt":"a", "max_tokens":10},
    {"prompt":"b", "max_tokens":10},
  ])
  return {"one": one["response"], "n": len(batch)}
`, Tier: "full"}
	res, err := rt.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	m, _ := res.Result.(map[string]any)
	if m["one"] != "summary" {
		t.Fatalf("llm_call response mismatch: %+v", m)
	}
	if got, ok := m["n"].(int64); !ok || got != 2 {
		t.Fatalf("llm_batch count mismatch: %+v", m)
	}
	if res.Stats.Steps != 2 {
		t.Fatalf("expected 2 steps (llm_call + llm_batch), got %d", res.Stats.Steps)
	}
}

func TestRuntimeMaxResultBytesEnforced(t *testing.T) {
	st := &executionState{
		cfg:   Config{MaxResultBytes: 8},
		trace: newTraceCollector(1024),
	}
	if err := st.validateResultSize(map[string]any{"payload": "this-is-too-big"}); err == nil {
		t.Fatalf("expected max_result_bytes validation error")
	}
}

func TestExecuteScriptToolFactory(t *testing.T) {
	tool := NewTool(ai.Model{}, testCatalog())
	res, err := tool.Execute(context.Background(), "id-1", map[string]any{"code": "def main(args):\n  return 42"}, nil)
	if err != nil {
		t.Fatalf("execute_script: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("expected single text block")
	}
	txt, ok := res.Content[0].(*ai.TextContent)
	if !ok {
		t.Fatalf("unexpected block type: %T", res.Content[0])
	}
	var payload ExecuteResult
	if err := json.Unmarshal([]byte(txt.Text), &payload); err != nil {
		t.Fatalf("unmarshal execute_script payload: %v", err)
	}
}
