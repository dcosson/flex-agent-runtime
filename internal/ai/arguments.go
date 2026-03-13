package ai

import (
	"encoding/json"
	"fmt"
	"io"
)

// LargeArgumentThreshold is the byte size above which provider implementations
// should prefer UnmarshalArgumentsFromReader (streaming from io.Reader) over
// UnmarshalArguments (from []byte). §17.3: 64KB threshold.
const LargeArgumentThreshold = 64 * 1024

// UnmarshalArguments parses JSON tool call arguments from a byte slice.
// Always uses json.Unmarshal since the data is already in memory.
// For payloads exceeding LargeArgumentThreshold, provider implementations
// should prefer UnmarshalArgumentsFromReader to stream directly from the
// response body and avoid materializing the full JSON in a []byte first.
func UnmarshalArguments(data []byte) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal(data, &args); err != nil {
		return nil, fmt.Errorf("invalid tool arguments JSON: %w", err)
	}
	return args, nil
}

// UnmarshalArgumentsFromReader parses tool call arguments from a reader
// using json.NewDecoder for streaming. This is the preferred path for
// provider implementations when ToolCall.Arguments JSON exceeds
// LargeArgumentThreshold (64KB), as it reads in 4KB chunks without
// materializing the entire JSON payload in a contiguous byte slice.
func UnmarshalArgumentsFromReader(r io.Reader) (map[string]any, error) {
	dec := json.NewDecoder(r)
	var args map[string]any
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("invalid tool arguments JSON: %w", err)
	}
	return args, nil
}
