package termmux

import (
	"sort"
	"sync"
	"testing"
)

func TestClient_Send(t *testing.T) {
	c := NewClient("test-1")
	defer c.Close()

	data := []byte("hello")
	c.Send(data)

	select {
	case got := <-c.Output():
		if string(got) != "hello" {
			t.Errorf("expected hello, got %s", string(got))
		}
	default:
		t.Error("expected data on output channel")
	}
}

func TestClient_SendAfterClose(t *testing.T) {
	c := NewClient("test-1")
	c.Close()

	// Should not panic
	c.Send([]byte("hello"))
}

func TestClient_DoubleClose(t *testing.T) {
	c := NewClient("test-1")
	c.Close()
	// Should not panic
	c.Close()
}

func TestClientManager_AttachDetach(t *testing.T) {
	cm := NewClientManager()

	c1 := cm.Attach("client-1")
	c2 := cm.Attach("client-2")

	if cm.Count() != 2 {
		t.Errorf("expected 2 clients, got %d", cm.Count())
	}

	_ = c1
	_ = c2

	cm.Detach("client-1")
	if cm.Count() != 1 {
		t.Errorf("expected 1 client after detach, got %d", cm.Count())
	}

	cm.Detach("client-2")
	if cm.Count() != 0 {
		t.Errorf("expected 0 clients after detach, got %d", cm.Count())
	}
}

func TestClientManager_ReattachClosesOld(t *testing.T) {
	cm := NewClientManager()

	c1 := cm.Attach("client-1")
	c2 := cm.Attach("client-1") // Same ID — should close c1

	// c1's output channel should be closed
	_, ok := <-c1.Output()
	if ok {
		t.Error("old client's channel should be closed")
	}

	// c2 should work
	c2.Send([]byte("test"))
	select {
	case got := <-c2.Output():
		if string(got) != "test" {
			t.Errorf("expected test, got %s", string(got))
		}
	default:
		t.Error("new client should receive data")
	}

	cm.CloseAll()
}

func TestClientManager_FanOut(t *testing.T) {
	cm := NewClientManager()

	c1 := cm.Attach("client-1")
	c2 := cm.Attach("client-2")

	cm.FanOut([]byte("broadcast"))

	select {
	case got := <-c1.Output():
		if string(got) != "broadcast" {
			t.Errorf("c1: expected broadcast, got %s", string(got))
		}
	default:
		t.Error("c1 should receive broadcast")
	}

	select {
	case got := <-c2.Output():
		if string(got) != "broadcast" {
			t.Errorf("c2: expected broadcast, got %s", string(got))
		}
	default:
		t.Error("c2 should receive broadcast")
	}

	cm.CloseAll()
}

func TestClientManager_FanOutNonBlocking(t *testing.T) {
	cm := NewClientManager()

	// Attach a client with a tiny buffer
	_ = cm.Attach("slow")

	// Fill the buffer
	for i := 0; i < defaultClientOutputBuffer+10; i++ {
		cm.FanOut([]byte("x"))
	}
	// Should not block or panic
}

func TestClientManager_List(t *testing.T) {
	cm := NewClientManager()

	cm.Attach("b")
	cm.Attach("a")
	cm.Attach("c")

	ids := cm.List()
	sort.Strings(ids)
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "b" || ids[2] != "c" {
		t.Errorf("unexpected client list: %v", ids)
	}

	cm.CloseAll()
}

func TestClientManager_CloseAll(t *testing.T) {
	cm := NewClientManager()

	c1 := cm.Attach("c1")
	c2 := cm.Attach("c2")

	cm.CloseAll()

	if cm.Count() != 0 {
		t.Errorf("expected 0 clients after CloseAll, got %d", cm.Count())
	}

	// Channels should be closed
	_, ok1 := <-c1.Output()
	_, ok2 := <-c2.Output()
	if ok1 || ok2 {
		t.Error("channels should be closed after CloseAll")
	}
}

func TestClientManager_ConcurrentAttachDetach(t *testing.T) {
	cm := NewClientManager()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cid := "client-" + string(rune('a'+id%26))
			cm.Attach(cid)
			cm.FanOut([]byte("data"))
			cm.Detach(cid)
		}(i)
	}

	wg.Wait()
}
