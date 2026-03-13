package ai

import "testing"

// B6: CalculateCost benchmark.
func BenchmarkCalculateCost(b *testing.B) {
	model := Model{Cost: ModelCost{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}}
	usage := &Usage{Input: 1000, Output: 500, CacheRead: 200, CacheWrite: 100}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		CalculateCost(model, usage)
	}
}

// B7: model lookup benchmark.
func BenchmarkGetModel(b *testing.B) {
	ClearModels()
	RegisterModel(Model{ID: "m", Provider: "p", API: "anthropic-messages"})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GetModel("p", "m")
	}
}
