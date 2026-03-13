package codeinterp

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

type traceCollector struct {
	steps     []TraceStep
	maxBytes  int
	currBytes int
	truncated bool
	mu        sync.Mutex
}

func newTraceCollector(max int) *traceCollector {
	return &traceCollector{maxBytes: max, steps: make([]TraceStep, 0, 32)}
}

func (t *traceCollector) add(step TraceStep) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.truncated {
		return
	}
	step = redactTraceStep(step)
	b, _ := json.Marshal(step)
	t.currBytes += len(b)
	if t.maxBytes > 0 && t.currBytes > t.maxBytes {
		t.truncated = true
		return
	}
	t.steps = append(t.steps, step)
}

func (t *traceCollector) snapshot() []TraceStep {
	t.mu.Lock()
	defer t.mu.Unlock()
	cp := make([]TraceStep, len(t.steps))
	copy(cp, t.steps)
	return cp
}

func (t *traceCollector) isTruncated() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.truncated
}

var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(secret[_-]?token\s*=\s*)([^\s,;]+)`),
	regexp.MustCompile(`(?i)(api[_-]?key\s*=\s*)([^\s,;]+)`),
	regexp.MustCompile(`(?i)(password\s*=\s*)([^\s,;]+)`),
}

func redactTraceStep(step TraceStep) TraceStep {
	step.Error = redactString(step.Error)
	if step.Input != nil {
		step.Input = redactMap(step.Input)
	}
	step.Output = redactAny(step.Output)
	return step
}

func redactAny(v any) any {
	switch x := v.(type) {
	case string:
		return redactString(x)
	case map[string]any:
		return redactMap(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = redactAny(x[i])
		}
		return out
	default:
		return v
	}
}

func redactMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = redactAny(v)
	}
	return out
}

func redactString(s string) string {
	out := s
	for _, re := range sensitivePatterns {
		out = re.ReplaceAllString(out, `$1[REDACTED]`)
	}
	if strings.Contains(strings.ToUpper(out), "SECRET") && strings.Contains(out, "=") {
		parts := strings.SplitN(out, "=", 2)
		if len(parts) == 2 {
			return parts[0] + "=[REDACTED]"
		}
	}
	return out
}
