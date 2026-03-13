package codeinterp

import (
	"encoding/json"
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
