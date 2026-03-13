package anthropic

import (
	"bytes"
	"fmt"
	"strings"

	streamingjson "github.com/karminski/streaming-json-go"

	"h2-agent-runtime/internal/ai"
)

type toolJSONParser struct {
	lexer *streamingjson.Lexer
	raw   strings.Builder
}

func newToolJSONParser() *toolJSONParser {
	return &toolJSONParser{lexer: streamingjson.NewLexer()}
}

func (p *toolJSONParser) AppendDelta(chunk string) (map[string]any, bool, error) {
	p.raw.WriteString(chunk)
	if err := p.lexer.AppendString(chunk); err != nil {
		return nil, false, fmt.Errorf("append tool delta: %w", err)
	}
	completed := p.lexer.CompleteJSON()
	if completed == "" {
		return nil, false, nil
	}
	args, err := ai.UnmarshalArguments([]byte(completed))
	if err != nil {
		return nil, false, nil
	}
	return cloneMap(args), true, nil
}

// ParseFinal strictly parses the fully received JSON and does not fallback to
// lastValid snapshots. This is intentionally strict to avoid silent truncation.
func (p *toolJSONParser) ParseFinal() (map[string]any, error) {
	data := p.raw.String()
	if strings.TrimSpace(data) == "" {
		return map[string]any{}, nil
	}
	var (
		args map[string]any
		err  error
	)
	if len(data) > ai.LargeArgumentThreshold {
		args, err = ai.UnmarshalArgumentsFromReader(strings.NewReader(data))
	} else {
		args, err = ai.UnmarshalArguments(bytes.Clone([]byte(data)))
	}
	if err != nil {
		return nil, err
	}
	if args == nil {
		return map[string]any{}, nil
	}
	return args, nil
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch x := v.(type) {
	case nil, bool, string, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return x
	case map[string]any:
		return cloneMap(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = cloneValue(x[i])
		}
		return out
	default:
		return x
	}
}
