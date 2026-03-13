package ai

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestUnmarshalArgumentsSmall(t *testing.T) {
	data := []byte(`{"query":"test","limit":10,"filters":{"category":"books"}}`)
	args, err := UnmarshalArguments(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["query"] != "test" {
		t.Fatalf("expected query=test, got %v", args["query"])
	}
	if args["limit"] != float64(10) {
		t.Fatalf("expected limit=10, got %v", args["limit"])
	}
}

func TestUnmarshalArgumentsLarge(t *testing.T) {
	data := generateLargeArguments(128 * 1024) // 128KB
	args, err := UnmarshalArguments(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected items array, got %v", args["items"])
	}
}

func TestUnmarshalArgumentsFromReader(t *testing.T) {
	data := []byte(`{"key":"value"}`)
	args, err := UnmarshalArgumentsFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args["key"] != "value" {
		t.Fatalf("expected key=value, got %v", args["key"])
	}
}

func TestUnmarshalArgumentsFromReaderLarge(t *testing.T) {
	data := generateLargeArguments(128 * 1024)
	args, err := UnmarshalArgumentsFromReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	items, ok := args["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected items array, got %v", args["items"])
	}
}

func TestUnmarshalArgumentsInvalidJSON(t *testing.T) {
	_, err := UnmarshalArguments([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestUnmarshalArgumentsFromReaderInvalidJSON(t *testing.T) {
	_, err := UnmarshalArgumentsFromReader(strings.NewReader(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON from reader")
	}
}

// §17.3: Benchmark comparing []byte vs streaming reader paths.
// Key insight: UnmarshalArguments (from []byte) is faster when data is
// already in memory. UnmarshalArgumentsFromReader is the path providers
// should use for large arguments read directly from HTTP response bodies.
func BenchmarkUnmarshalArguments(b *testing.B) {
	b.Run("small_from_bytes", func(b *testing.B) {
		data := []byte(`{"query":"test","limit":10,"filters":{"category":"books"}}`)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = UnmarshalArguments(data)
		}
	})

	b.Run("small_from_reader", func(b *testing.B) {
		data := []byte(`{"query":"test","limit":10,"filters":{"category":"books"}}`)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = UnmarshalArgumentsFromReader(bytes.NewReader(data))
		}
	})

	b.Run("large_128KB_from_bytes", func(b *testing.B) {
		data := generateLargeArguments(128 * 1024)
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = UnmarshalArguments(data)
		}
	})

	b.Run("large_128KB_from_reader", func(b *testing.B) {
		data := generateLargeArguments(128 * 1024)
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = UnmarshalArgumentsFromReader(bytes.NewReader(data))
		}
	})

	b.Run("large_512KB_from_bytes", func(b *testing.B) {
		data := generateLargeArguments(512 * 1024)
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = UnmarshalArguments(data)
		}
	})

	b.Run("large_512KB_from_reader", func(b *testing.B) {
		data := generateLargeArguments(512 * 1024)
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = UnmarshalArgumentsFromReader(bytes.NewReader(data))
		}
	})
}

// generateLargeArguments creates a JSON object with an items array sized
// to exceed the given byte target.
func generateLargeArguments(targetBytes int) []byte {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	i := 0
	for b.Len() < targetBytes {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"name":"item-%d","description":"A test item with some descriptive text to pad the payload size out to the target","tags":["alpha","beta","gamma"]}`, i, i)
		i++
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}
