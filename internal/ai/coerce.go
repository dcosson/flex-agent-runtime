package ai

import (
	"encoding/json"
	"strconv"
)

// schemaProps extracts the "properties" map and their types from a JSON Schema.
// Returns property name → schema type string (e.g., "integer", "number", "boolean", "null", "object", "array").
// Also returns sub-schema bytes for nested objects and array items.
type propInfo struct {
	typ        string
	subSchema  json.RawMessage // for "object" type: the full sub-schema
	itemSchema json.RawMessage // for "array" type: the items sub-schema
}

func extractProps(schema json.RawMessage) map[string]propInfo {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &s); err != nil || s.Properties == nil {
		return nil
	}

	result := make(map[string]propInfo, len(s.Properties))
	for name, raw := range s.Properties {
		var prop struct {
			Type  string          `json:"type"`
			Items json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(raw, &prop); err != nil {
			continue
		}
		result[name] = propInfo{
			typ:        prop.Type,
			subSchema:  raw,
			itemSchema: prop.Items,
		}
	}
	return result
}

// CoerceTypes attempts to coerce argument values to match their JSON Schema types.
// This handles LLM output that sends "123" instead of 123 for integer fields.
// Modifies args in-place. Returns the (possibly modified) args map.
//
// Coercion rules:
//   - string → integer: strconv.Atoi
//   - string → number: strconv.ParseFloat
//   - string → boolean: strconv.ParseBool
//   - string → null: "null" literal → nil
//   - object: recurse into sub-schema
//   - array: coerce each element according to items schema
//
// On coercion failure, the original value is left unchanged (validation will catch it).
func CoerceTypes(schema json.RawMessage, args map[string]any) map[string]any {
	props := extractProps(schema)
	if props == nil {
		return args
	}

	for name, info := range props {
		val, exists := args[name]
		if !exists {
			continue
		}

		args[name] = coerceValue(val, info)
	}

	return args
}

func coerceValue(val any, info propInfo) any {
	switch info.typ {
	case "integer":
		return coerceToInteger(val)
	case "number":
		return coerceToNumber(val)
	case "boolean":
		return coerceToBoolean(val)
	case "null":
		return coerceToNull(val)
	case "object":
		return coerceToObject(val, info.subSchema)
	case "array":
		return coerceToArray(val, info.itemSchema)
	default:
		return val
	}
}

func coerceToInteger(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return val
	}
	return i
}

func coerceToNumber(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return val
	}
	return f
}

func coerceToBoolean(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return val
	}
	return b
}

func coerceToNull(val any) any {
	s, ok := val.(string)
	if !ok {
		return val
	}
	if s == "null" {
		return nil
	}
	return val
}

func coerceToObject(val any, subSchema json.RawMessage) any {
	m, ok := val.(map[string]any)
	if !ok || subSchema == nil {
		return val
	}
	return CoerceTypes(subSchema, m)
}

func coerceToArray(val any, itemSchema json.RawMessage) any {
	arr, ok := val.([]any)
	if !ok || itemSchema == nil {
		return val
	}

	var item struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(itemSchema, &item); err != nil {
		return val
	}

	info := propInfo{typ: item.Type, subSchema: itemSchema}
	for i, elem := range arr {
		arr[i] = coerceValue(elem, info)
	}
	return arr
}
