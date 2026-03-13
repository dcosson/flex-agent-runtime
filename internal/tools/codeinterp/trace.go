package codeinterp

import (
	"encoding/json"
)

type traceCollector struct {
	steps     []TraceStep
	maxBytes  int
	currBytes int
	truncated bool
}

func newTraceCollector(max int) *traceCollector {
	return &traceCollector{maxBytes: max, steps: make([]TraceStep, 0, 32)}
}

func (t *traceCollector) add(step TraceStep) {
	if t.truncated {
		return
	}
	b, _ := json.Marshal(step)
	t.currBytes += len(b)
	if t.maxBytes > 0 && t.currBytes > t.maxBytes {
		t.truncated = true
		return
	}
	t.steps = append(t.steps, step)
}
