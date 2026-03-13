package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateToolArguments_ValidArgs(t *testing.T) {
	tool := Tool{
		Name: "search",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string"},
				"limit": {"type": "integer"}
			},
			"required": ["query"]
		}`),
	}
	args := map[string]any{"query": "test", "limit": 10}
	if err := ValidateToolArguments(tool, args); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateToolArguments_MissingRequired(t *testing.T) {
	tool := Tool{
		Name: "search",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string"}
			},
			"required": ["query"]
		}`),
	}
	args := map[string]any{"limit": 10}
	err := ValidateToolArguments(tool, args)
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	t.Logf("got expected error: %v", err)
}

func TestValidateToolArguments_InvalidType(t *testing.T) {
	tool := Tool{
		Name: "search",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"limit": {"type": "integer"}
			}
		}`),
	}
	// Non-coercible string → should fail validation.
	args := map[string]any{"limit": "not-a-number"}
	err := ValidateToolArguments(tool, args)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestValidateToolArguments_Coercion(t *testing.T) {
	tool := Tool{
		Name: "search",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string"},
				"limit": {"type": "integer"},
				"score": {"type": "number"},
				"verbose": {"type": "boolean"}
			},
			"required": ["query"]
		}`),
	}
	args := map[string]any{
		"query":   "test",
		"limit":   "42",
		"score":   "3.14",
		"verbose": "true",
	}
	if err := ValidateToolArguments(tool, args); err != nil {
		t.Fatalf("expected valid after coercion, got: %v", err)
	}
	// Verify coerced values were written back.
	if args["limit"] != 42 {
		t.Errorf("limit: expected 42, got %v (%T)", args["limit"], args["limit"])
	}
	if args["score"] != 3.14 {
		t.Errorf("score: expected 3.14, got %v", args["score"])
	}
	if args["verbose"] != true {
		t.Errorf("verbose: expected true, got %v", args["verbose"])
	}
}

func TestValidateToolArguments_NoSchema(t *testing.T) {
	tool := Tool{Name: "noop"}
	args := map[string]any{"anything": "goes"}
	if err := ValidateToolArguments(tool, args); err != nil {
		t.Fatalf("expected nil for empty schema, got: %v", err)
	}
}

func TestValidateToolCall_Found(t *testing.T) {
	tools := []Tool{
		{Name: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		{Name: "write", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	}
	tc := ToolCall{Name: "read", Arguments: map[string]any{"path": "/tmp/foo"}}
	if err := ValidateToolCall(tools, tc); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateToolCall_UnknownTool(t *testing.T) {
	tools := []Tool{
		{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	tc := ToolCall{Name: "delete", Arguments: map[string]any{}}
	err := ValidateToolCall(tools, tc)
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if want := `unknown tool: "delete"`; err.Error() != want {
		t.Errorf("got error %q, want %q", err.Error(), want)
	}
}

func TestCoerceTypes_NestedObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"filters": {
				"type": "object",
				"properties": {
					"count": {"type": "integer"}
				}
			}
		}
	}`)
	args := map[string]any{
		"filters": map[string]any{
			"count": "5",
		},
	}
	CoerceTypes(schema, args)
	filters := args["filters"].(map[string]any)
	if filters["count"] != 5 {
		t.Errorf("nested count: expected 5, got %v (%T)", filters["count"], filters["count"])
	}
}

func TestCoerceTypes_Array(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"ids": {
				"type": "array",
				"items": {"type": "integer"}
			}
		}
	}`)
	args := map[string]any{
		"ids": []any{"1", "2", "3"},
	}
	CoerceTypes(schema, args)
	ids := args["ids"].([]any)
	for i, expected := range []int{1, 2, 3} {
		if ids[i] != expected {
			t.Errorf("ids[%d]: expected %d, got %v (%T)", i, expected, ids[i], ids[i])
		}
	}
}

func TestCoerceTypes_NullCoercion(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"value": {"type": "null"}
		}
	}`)
	args := map[string]any{"value": "null"}
	CoerceTypes(schema, args)
	if args["value"] != nil {
		t.Errorf("expected nil, got %v", args["value"])
	}
}

func TestCoerceTypes_PreservesValidTypes(t *testing.T) {
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
		"name":  "hello",
		"count": 42,
		"rate":  3.14,
		"flag":  true,
	}
	CoerceTypes(schema, args)
	if args["name"] != "hello" {
		t.Errorf("name: expected hello, got %v", args["name"])
	}
	if args["count"] != 42 {
		t.Errorf("count: expected 42, got %v (%T)", args["count"], args["count"])
	}
	if args["rate"] != 3.14 {
		t.Errorf("rate: expected 3.14, got %v", args["rate"])
	}
	if args["flag"] != true {
		t.Errorf("flag: expected true, got %v", args["flag"])
	}
}

func TestCoerceTypes_FailurePreservesOriginal(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"count": {"type": "integer"}
		}
	}`)
	args := map[string]any{"count": "not-a-number"}
	CoerceTypes(schema, args)
	if args["count"] != "not-a-number" {
		t.Errorf("expected original value preserved, got %v", args["count"])
	}
}

func TestSchemaCache(t *testing.T) {
	ClearSchemaCache()

	tool := Tool{
		Name:       "test",
		Parameters: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}}}`),
	}
	args := map[string]any{"x": "hello"}

	// First call: cache miss.
	if err := ValidateToolArguments(tool, args); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, misses := SchemaCacheStats()
	if misses != 1 {
		t.Errorf("expected 1 miss, got %d", misses)
	}

	// Second call: cache hit.
	if err := ValidateToolArguments(tool, args); err != nil {
		t.Fatalf("second call: %v", err)
	}
	hits, _ := SchemaCacheStats()
	if hits != 1 {
		t.Errorf("expected 1 hit, got %d", hits)
	}
}

func TestFormatValidationError(t *testing.T) {
	tool := Tool{
		Name: "test",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"age": {"type": "integer"}
			},
			"required": ["name", "age"]
		}`),
	}
	args := map[string]any{} // missing both required fields
	err := ValidateToolArguments(tool, args)
	if err == nil {
		t.Fatal("expected error")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "test") {
		t.Errorf("error should mention tool name: %s", errMsg)
	}
}
