package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type mockProvider struct{ api string }

func (m *mockProvider) API() string { return m.api }
func (m *mockProvider) Stream(_ context.Context, _ Model, _ Context, _ StreamOptions) *EventStream {
	es := NewEventStream()
	go func() {
		defer es.Close()
		es.Send(AssistantMessageEvent{Type: EventDone, Message: &AssistantMessage{Model: "ok"}})
	}()
	return es
}
func (m *mockProvider) StreamSimple(_ context.Context, _ Model, _ Context, _ SimpleStreamOptions) *EventStream {
	return m.Stream(context.Background(), Model{}, Context{}, StreamOptions{})
}

func TestProviderRegistryBasic(t *testing.T) {
	ClearProviders()
	p := &mockProvider{api: "x"}
	RegisterProvider(p, "src")
	got, err := GetProvider("x")
	if err != nil {
		t.Fatalf("GetProvider err: %v", err)
	}
	if got.API() != "x" {
		t.Fatalf("unexpected provider")
	}
	if len(GetProviders()) != 1 {
		t.Fatalf("expected 1 provider")
	}
	UnregisterProviders("src")
	if _, err := GetProvider("x"); err == nil {
		t.Fatalf("expected missing provider after unregister")
	}
}

// P9: registry thread-safety under concurrent operations.
func TestRegistryConcurrentAccess(t *testing.T) {
	ClearProviders()
	const goroutines = 50
	const ops = 1000

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < ops; j++ {
				api := fmt.Sprintf("api-%d", id%10)
				switch j % 4 {
				case 0:
					RegisterProvider(&mockProvider{api: api}, "test")
				case 1:
					_, _ = GetProvider(api)
				case 2:
					_ = GetProviders()
				case 3:
					UnregisterProviders("test")
				}
			}
		}(i)
	}
	wg.Wait()
}

// S2: stress registry under contention for a bounded duration.
func TestRegistryStressContention(t *testing.T) {
	ClearProviders()
	deadline := time.Now().Add(750 * time.Millisecond)
	var wg sync.WaitGroup

	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			n := 0
			for time.Now().Before(deadline) {
				api := fmt.Sprintf("p-%d-%d", id, n%8)
				RegisterProvider(&mockProvider{api: api}, "stress")
				_, _ = GetProvider(api)
				_ = GetProviders()
				n++
			}
		}(g)
	}
	wg.Wait()
}

func TestStreamEntryPoints(t *testing.T) {
	ClearProviders()
	RegisterProvider(&mockProvider{api: "anthropic-messages"}, "test")
	model := Model{API: "anthropic-messages"}
	msg, err := Complete(context.Background(), model, Context{}, StreamOptions{})
	if err != nil {
		t.Fatalf("Complete err: %v", err)
	}
	if msg.Model != "ok" {
		t.Fatalf("unexpected model: %q", msg.Model)
	}

	missing := Model{API: "missing"}
	es := Stream(context.Background(), missing, Context{}, StreamOptions{})
	_, err = es.Drain()
	if err == nil {
		t.Fatalf("expected error stream")
	}
}
