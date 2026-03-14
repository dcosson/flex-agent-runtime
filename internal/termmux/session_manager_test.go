package termmux

import (
	"context"
	"testing"
	"time"
)

func TestSessionManager_CreateAndGet(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Shutdown(5 * time.Second)

	s, err := sm.Create("s1", SessionConfig{
		Command: "/bin/echo",
		Args:    []string{"hello"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := sm.Get("s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != s {
		t.Error("Get returned different session")
	}
}

func TestSessionManager_CreateDuplicate(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Shutdown(5 * time.Second)

	_, err := sm.Create("s1", SessionConfig{Command: "/bin/echo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = sm.Create("s1", SessionConfig{Command: "/bin/echo"})
	if err == nil {
		t.Error("expected error on duplicate session ID")
	}
}

func TestSessionManager_CapacityLimit(t *testing.T) {
	sm := NewSessionManager(WithMaxSessions(2))
	defer sm.Shutdown(5 * time.Second)

	_, err := sm.Create("s1", SessionConfig{Command: "/bin/echo"})
	if err != nil {
		t.Fatalf("Create s1: %v", err)
	}
	_, err = sm.Create("s2", SessionConfig{Command: "/bin/echo"})
	if err != nil {
		t.Fatalf("Create s2: %v", err)
	}

	_, err = sm.Create("s3", SessionConfig{Command: "/bin/echo"})
	if err == nil {
		t.Error("expected error when exceeding capacity")
	}
}

func TestSessionManager_Kill(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Shutdown(5 * time.Second)

	s, err := sm.Create("s1", SessionConfig{
		Command: "/bin/sleep",
		Args:    []string{"300"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := sm.Kill("s1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if sm.Count() != 0 {
		t.Errorf("expected 0 sessions after kill, got %d", sm.Count())
	}

	// Kill non-existent
	if err := sm.Kill("nonexistent"); err == nil {
		t.Error("expected error killing non-existent session")
	}
}

func TestSessionManager_List(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Shutdown(5 * time.Second)

	_, _ = sm.Create("s1", SessionConfig{Command: "/bin/echo", DriverType: "test"})
	time.Sleep(time.Millisecond) // Ensure different creation times
	_, _ = sm.Create("s2", SessionConfig{Command: "/bin/echo", DriverType: "test"})

	infos := sm.List()
	if len(infos) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(infos))
	}

	// Should be sorted by creation time
	if infos[0].ID != "s1" {
		t.Errorf("expected first session to be s1, got %s", infos[0].ID)
	}
	if infos[1].ID != "s2" {
		t.Errorf("expected second session to be s2, got %s", infos[1].ID)
	}
}

func TestSessionManager_GetNotFound(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Shutdown(5 * time.Second)

	_, err := sm.Get("nonexistent")
	if err == nil {
		t.Error("expected error on non-existent session")
	}
}

func TestSessionManager_Count(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Shutdown(5 * time.Second)

	if sm.Count() != 0 {
		t.Errorf("expected 0 sessions, got %d", sm.Count())
	}

	_, _ = sm.Create("s1", SessionConfig{Command: "/bin/echo"})
	if sm.Count() != 1 {
		t.Errorf("expected 1 session, got %d", sm.Count())
	}

	_, _ = sm.Create("s2", SessionConfig{Command: "/bin/echo"})
	if sm.Count() != 2 {
		t.Errorf("expected 2 sessions, got %d", sm.Count())
	}
}

func TestSessionManager_Shutdown(t *testing.T) {
	sm := NewSessionManager()

	s1, _ := sm.Create("s1", SessionConfig{
		Command: "/bin/sleep",
		Args:    []string{"300"},
	})
	s2, _ := sm.Create("s2", SessionConfig{
		Command: "/bin/sleep",
		Args:    []string{"300"},
	})

	if err := s1.Start(context.Background()); err != nil {
		t.Fatalf("Start s1: %v", err)
	}
	if err := s2.Start(context.Background()); err != nil {
		t.Fatalf("Start s2: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	err := sm.Shutdown(10 * time.Second)
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if sm.Count() != 0 {
		t.Errorf("expected 0 sessions after shutdown, got %d", sm.Count())
	}
}

func TestSessionManager_ShutdownEmpty(t *testing.T) {
	sm := NewSessionManager()
	err := sm.Shutdown(5 * time.Second)
	if err != nil {
		t.Fatalf("Shutdown empty: %v", err)
	}
}
