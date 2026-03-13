package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// =============================================================================
// P4: CoerceTypes Preserves Valid Types (Property-Based)
// =============================================================================

func TestP4_CoerceTypesPreservesValidTypes(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// Generate valid typed values and verify CoerceTypes doesn't change them.
		name := rapid.String().Draw(t, "name")
		count := rapid.Int().Draw(t, "count")
		rate := rapid.Float64().Draw(t, "rate")
		flag := rapid.Bool().Draw(t, "flag")

		schema := json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"count": {"type": "integer"},
				"rate": {"type": "number"},
				"flag": {"type": "boolean"}
			}
		}`)

		args := map[string]any{
			"name":  name,
			"count": count,
			"rate":  rate,
			"flag":  flag,
		}

		CoerceTypes(schema, args)

		if args["name"] != name {
			t.Errorf("name changed: %v → %v", name, args["name"])
		}
		if args["count"] != count {
			t.Errorf("count changed: %v → %v", count, args["count"])
		}
		if args["rate"] != rate {
			t.Errorf("rate changed: %v → %v", rate, args["rate"])
		}
		if args["flag"] != flag {
			t.Errorf("flag changed: %v → %v", flag, args["flag"])
		}
	})
}

// =============================================================================
// F2: JSON Schema Validation Fuzzing
// =============================================================================

func FuzzValidateToolArguments(f *testing.F) {
	schema := `{"type":"object","properties":{"name":{"type":"string"},"count":{"type":"integer"}},"required":["name"]}`

	f.Add(`{"name":"test","count":5}`)
	f.Add(`{"name":"test"}`)
	f.Add(`{}`)
	f.Add(`{"name":123}`)
	f.Add(`{"name":"","count":"42"}`)
	f.Add(`{"name":"x","count":"abc"}`)

	f.Fuzz(func(t *testing.T, argsJSON string) {
		var args map[string]any
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return // skip non-JSON inputs
		}
		tool := Tool{
			Name:       "test",
			Parameters: json.RawMessage(schema),
		}
		// Must not panic.
		_ = ValidateToolArguments(tool, args)
	})
}

// =============================================================================
// F3: CoerceTypes Fuzzing
// =============================================================================

func FuzzCoerceTypes(f *testing.F) {
	schema := `{"type":"object","properties":{"x":{"type":"integer"},"y":{"type":"number"},"z":{"type":"boolean"}}}`

	f.Add(`{"x":"42","y":"3.14","z":"true"}`)
	f.Add(`{"x":"abc","y":"def","z":"ghi"}`)
	f.Add(`{}`)
	f.Add(`{"x":42}`)
	f.Add(`{"x":"","y":"","z":""}`)

	f.Fuzz(func(t *testing.T, argsJSON string) {
		var args map[string]any
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return
		}
		// Must not panic. Must not produce values that violate the schema
		// worse than the input (hard to check generically, so we just check no panic).
		CoerceTypes(json.RawMessage(schema), args)
	})
}

// =============================================================================
// S3: Schema Cache Under Varied Load
// =============================================================================

func TestS3_SchemaCacheVariedLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in short mode")
	}

	t.Run("cold_cache_1000_unique", func(t *testing.T) {
		ClearSchemaCache()
		for i := 0; i < 1000; i++ {
			schema := json.RawMessage(fmt.Sprintf(
				`{"type":"object","properties":{"field_%d":{"type":"string"}}}`, i,
			))
			_, err := compileSchema(schema)
			if err != nil {
				t.Fatalf("compilation failed at i=%d: %v", i, err)
			}
		}
		hits, misses := SchemaCacheStats()
		if misses != 1000 {
			t.Errorf("expected 1000 misses, got %d", misses)
		}
		if hits != 0 {
			t.Errorf("expected 0 hits, got %d", hits)
		}
	})

	t.Run("hot_cache_1000_repeated", func(t *testing.T) {
		ClearSchemaCache()
		schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`)

		// Prime cache.
		_, _ = compileSchema(schema)

		for i := 0; i < 999; i++ {
			_, err := compileSchema(schema)
			if err != nil {
				t.Fatalf("compilation failed at i=%d: %v", i, err)
			}
		}
		hits, misses := SchemaCacheStats()
		if misses != 1 {
			t.Errorf("expected 1 miss, got %d", misses)
		}
		if hits != 999 {
			t.Errorf("expected 999 hits, got %d", hits)
		}
	})

	t.Run("mixed_80_20", func(t *testing.T) {
		ClearSchemaCache()
		hotSchema := json.RawMessage(`{"type":"object","properties":{"hot":{"type":"string"}}}`)

		// Generate unique schemas for the 20% cold path.
		uniqueSchemas := make([]json.RawMessage, 200)
		for i := range uniqueSchemas {
			uniqueSchemas[i] = json.RawMessage(fmt.Sprintf(
				`{"type":"object","properties":{"cold_%d":{"type":"integer"}}}`, i,
			))
		}

		rng := rand.New(rand.NewSource(42))
		coldIdx := 0
		for i := 0; i < 1000; i++ {
			if rng.Float64() < 0.8 {
				_, _ = compileSchema(hotSchema)
			} else {
				if coldIdx < len(uniqueSchemas) {
					_, _ = compileSchema(uniqueSchemas[coldIdx])
					coldIdx++
				} else {
					_, _ = compileSchema(hotSchema)
				}
			}
		}

		hits, misses := SchemaCacheStats()
		t.Logf("mixed load: %d hits, %d misses (%.1f%% hit rate)",
			hits, misses, float64(hits)/float64(hits+misses)*100)

		// Hot schema should have many hits.
		if hits == 0 {
			t.Error("expected some cache hits in mixed load")
		}
	})

	t.Run("concurrent_access", func(t *testing.T) {
		ClearSchemaCache()
		var wg sync.WaitGroup
		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					schema := json.RawMessage(fmt.Sprintf(
						`{"type":"object","properties":{"g%d_f%d":{"type":"string"}}}`, g, i,
					))
					_, err := compileSchema(schema)
					if err != nil {
						t.Errorf("goroutine %d, i=%d: %v", g, i, err)
					}
				}
			}(g)
		}
		wg.Wait()
	})
}

// =============================================================================
// B4: JSON Schema Validation Benchmark (target: <50μs warm cache)
// =============================================================================

func BenchmarkValidateToolArguments(b *testing.B) {
	tool := Tool{
		Name: "search",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string"},
				"limit":{"type":"integer"},
				"filters":{"type":"object","properties":{"category":{"type":"string"}}}
			},
			"required":["query"]
		}`),
	}
	args := map[string]any{"query": "test", "limit": 10, "filters": map[string]any{"category": "books"}}

	// Warm the cache.
	_ = ValidateToolArguments(tool, args)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Re-create args each iteration to avoid coercion side effects.
		a := map[string]any{"query": "test", "limit": 10, "filters": map[string]any{"category": "books"}}
		ValidateToolArguments(tool, a)
	}
}

// =============================================================================
// B5: Schema Compilation (Cold Cache) Benchmark (target: <500μs)
// =============================================================================

func BenchmarkSchemaCompilationCold(b *testing.B) {
	schemas := make([]json.RawMessage, b.N)
	for i := range schemas {
		schemas[i] = json.RawMessage(fmt.Sprintf(
			`{"type":"object","properties":{"field_%d":{"type":"string"},"num_%d":{"type":"integer"}},"required":["field_%d"]}`,
			i, i, i,
		))
	}
	ClearSchemaCache()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = compileSchema(schemas[i])
	}
}

// =============================================================================
// Sec1: Malicious Schema Injection
// =============================================================================

func TestSec1_MaliciousSchemas(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{"recursive_ref", `{"$ref": "#"}`},
		{"deep_nesting", generateDeeplyNestedSchema(100)},
		{"huge_enum", `{"type":"string","enum":[` + generateLargeEnum(10000) + `]}`},
		{"regex_dos", `{"type":"string","pattern":"(a+)+$"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() {
				tool := Tool{Name: "test", Parameters: json.RawMessage(tc.schema)}
				done <- ValidateToolArguments(tool, map[string]any{"x": "y"})
			}()

			select {
			case <-ctx.Done():
				t.Fatal("schema validation timed out — possible DoS")
			case err := <-done:
				// Error is fine, timeout/panic is not.
				_ = err
			}
		})
	}
}

func generateDeeplyNestedSchema(depth int) string {
	var sb strings.Builder
	for i := 0; i < depth; i++ {
		sb.WriteString(fmt.Sprintf(`{"type":"object","properties":{"level_%d":`, i))
	}
	sb.WriteString(`{"type":"string"}`)
	for i := 0; i < depth; i++ {
		sb.WriteString(`}}`)
	}
	return sb.String()
}

func generateLargeEnum(count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = fmt.Sprintf(`"value_%d"`, i)
	}
	return strings.Join(parts, ",")
}
