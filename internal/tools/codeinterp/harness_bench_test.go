package codeinterp

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
	"github.com/dcosson/flex-agent-runtime/internal/tools/codeinterp/datastore"
)

func BenchmarkB1_ScriptStartupLatency(b *testing.B) {
	rt := NewRuntime(ai.Model{}, testCatalog())
	req := ExecuteRequest{Code: `def main(args): return 1`}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Execute(context.Background(), req); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}

func BenchmarkB2_BuiltinDispatchOverhead(b *testing.B) {
	rt := NewRuntime(ai.Model{}, testCatalog())
	req := ExecuteRequest{Code: `def main(args): discover("file"); return 1`}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Execute(context.Background(), req); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}

func BenchmarkB3_TraceSerializationCost(b *testing.B) {
	rt := NewRuntime(ai.Model{}, testCatalog())
	req := ExecuteRequest{Code: `
def main(args):
  i = 0
  while i < 50:
    log("x")
    i += 1
  return 1
`}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Execute(context.Background(), req); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}

func BenchmarkB4_RepeatedScriptRuns(b *testing.B) {
	rt := NewRuntime(ai.Model{}, testCatalog())
	req := ExecuteRequest{Code: `def main(args): return discover("f")`}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Execute(context.Background(), req); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}

func BenchmarkB5_RLMDispatchOverhead(b *testing.B) {
	model := registerBenchModel(b)
	rt := NewRuntime(model, testCatalog())
	req := ExecuteRequest{
		Tier: "full",
		Code: `def main(args): return llm_call("x", max_tokens=4)`,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rt.Execute(context.Background(), req); err != nil {
			b.Fatalf("execute: %v", err)
		}
	}
}

func BenchmarkB6_DataStoreThroughput(b *testing.B) {
	ds := datastore.NewMemoryDataStore(64*1024*1024, 128, 8*1024)
	payload := []byte("0123456789abcdef")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "k"
		if err := ds.Write(key, payload); err != nil {
			b.Fatalf("write: %v", err)
		}
		if _, err := ds.Read(key); err != nil {
			b.Fatalf("read: %v", err)
		}
	}
}

func registerBenchModel(b *testing.B) ai.Model {
	b.Helper()
	ai.ClearAPIClients()
	ai.ClearProviderConfigs()
	b.Cleanup(ai.ClearAPIClients)
	b.Cleanup(ai.ClearProviderConfigs)
	id := atomic.AddUint64(&harnessSeq, 1)
	clientType := fmt.Sprintf("codeinterp-bench-%d", id)
	providerName := fmt.Sprintf("bench-provider-%d", id)
	p := &harnessProvider{clientType: clientType, inTokens: 1, outTokens: 1, costUSD: 0.001}
	ai.RegisterAPIClient(p)
	ai.RegisterProviderConfig(ai.ProviderConfig{Name: providerName, APIClientType: clientType})
	m := ai.Model{ID: fmt.Sprintf("bench-%d", id), API: clientType, Provider: providerName, MaxTokens: 4096}
	ai.RegisterModel(m)
	return m
}
