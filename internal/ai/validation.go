package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cespare/xxhash/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var (
	schemaCacheMu sync.RWMutex
	schemaCache   = make(map[uint64]*jsonschema.Schema)

	// Cache metrics for observability (URP §16.1).
	schemaCacheHits   atomic.Int64
	schemaCacheMisses atomic.Int64
)

// SchemaCacheStats returns current cache hit/miss counts.
func SchemaCacheStats() (hits, misses int64) {
	return schemaCacheHits.Load(), schemaCacheMisses.Load()
}

// ClearSchemaCache removes all entries from the schema compilation cache.
// Intended for benchmarks and tests.
func ClearSchemaCache() {
	schemaCacheMu.Lock()
	schemaCache = make(map[uint64]*jsonschema.Schema)
	schemaCacheMu.Unlock()
	schemaCacheHits.Store(0)
	schemaCacheMisses.Store(0)
}

// compileSchema compiles a JSON Schema with caching.
// Uses xxhash of the raw schema bytes as cache key with double-checked locking.
func compileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	h := xxhash.Sum64(raw)

	// Fast path: read lock check.
	schemaCacheMu.RLock()
	if s, ok := schemaCache[h]; ok {
		schemaCacheMu.RUnlock()
		schemaCacheHits.Add(1)
		slog.Debug("schema cache hit", "hash", h)
		return s, nil
	}
	schemaCacheMu.RUnlock()

	// Slow path: compile and store.
	schemaCacheMisses.Add(1)
	slog.Debug("schema cache miss", "hash", h)

	c := jsonschema.NewCompiler()
	if err := c.AddResource("tool.json", unmarshalToAny(raw)); err != nil {
		return nil, err
	}
	s, err := c.Compile("tool.json")
	if err != nil {
		return nil, err
	}

	schemaCacheMu.Lock()
	// Double-checked: another goroutine may have compiled while we were compiling.
	if existing, ok := schemaCache[h]; ok {
		schemaCacheMu.Unlock()
		return existing, nil
	}
	schemaCache[h] = s
	schemaCacheMu.Unlock()
	return s, nil
}

// unmarshalToAny converts raw JSON to an any value suitable for jsonschema.
func unmarshalToAny(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

// ValidateToolArguments validates tool call arguments against the tool's JSON Schema.
// Arguments are coerced (e.g., string "123" → int 123) before validation.
// Returns nil if valid, or a descriptive error with paths.
func ValidateToolArguments(tool Tool, args map[string]any) error {
	if len(tool.Parameters) == 0 {
		return nil // no schema = no validation
	}

	coerced := CoerceTypes(tool.Parameters, args)

	schema, err := compileSchema(tool.Parameters)
	if err != nil {
		return fmt.Errorf("invalid tool schema for %q: %w", tool.Name, err)
	}

	// Convert coerced args to any for validation.
	if err := schema.Validate(any(coerced)); err != nil {
		return formatValidationError(tool.Name, args, err)
	}

	// Copy coerced values back into args (mutation, matching TS behavior).
	for k, v := range coerced {
		args[k] = v
	}
	return nil
}

// ValidateToolCall finds the tool by name and validates the tool call's arguments.
func ValidateToolCall(tools []Tool, tc ToolCall) error {
	for _, tool := range tools {
		if tool.Name == tc.Name {
			return ValidateToolArguments(tool, tc.Arguments)
		}
	}
	return fmt.Errorf("unknown tool: %q", tc.Name)
}

// formatValidationError produces a clear error message with JSON paths
// using the BasicOutput format from the jsonschema library.
func formatValidationError(toolName string, _ map[string]any, err error) error {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return fmt.Errorf("validation failed for tool %q: %w", toolName, err)
	}

	output := ve.BasicOutput()
	var msgs []string
	collectOutputErrors(output, &msgs)

	if len(msgs) == 0 {
		return fmt.Errorf("validation failed for tool %q: %s", toolName, err.Error())
	}

	return fmt.Errorf("validation failed for tool %q:\n  %s", toolName, strings.Join(msgs, "\n  "))
}

func collectOutputErrors(unit *jsonschema.OutputUnit, msgs *[]string) {
	if unit.Error != nil {
		path := unit.InstanceLocation
		if path == "" {
			path = "/"
		}
		*msgs = append(*msgs, fmt.Sprintf("at %s: %s", path, unit.Error.String()))
	}
	for i := range unit.Errors {
		collectOutputErrors(&unit.Errors[i], msgs)
	}
}
