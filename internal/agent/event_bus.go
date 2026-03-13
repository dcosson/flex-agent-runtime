package agent

import "sync"

type eventBus struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]func(AgentEvent)
	closed bool
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[int]func(AgentEvent))}
}

func (b *eventBus) subscribe(fn func(AgentEvent)) (int, func()) {
	if fn == nil {
		return 0, func() {}
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, func() {}
	}
	id := b.nextID
	b.nextID++
	b.subs[id] = fn

	return id, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

func (b *eventBus) publish(evt AgentEvent) {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return
	}
	handlers := make([]func(AgentEvent), 0, len(b.subs))
	for _, fn := range b.subs {
		handlers = append(handlers, fn)
	}
	b.mu.RUnlock()

	for _, fn := range handlers {
		safeCallSubscriber(fn, evt)
	}
}

func (b *eventBus) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.subs = nil
}

func (b *eventBus) count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func safeCallSubscriber(fn func(AgentEvent), evt AgentEvent) {
	defer func() {
		if recover() != nil {
			// Intentionally swallow subscriber panics to isolate faults.
		}
	}()
	fn(evt.clone())
}
