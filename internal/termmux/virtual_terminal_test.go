package termmux

import (
	"sync"
	"testing"
	"time"
)

func TestVirtualTerminal_StartPTY(t *testing.T) {
	vt := &VirtualTerminal{}

	err := vt.StartPTY("/bin/echo", []string{"hello"}, 24, 80, nil)
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}

	// Wait for child to exit
	var gotData bool
	err = vt.PipeOutput(func(data []byte) {
		gotData = true
	})
	if err != nil {
		t.Fatalf("PipeOutput: %v", err)
	}

	if !gotData {
		t.Error("expected to receive data from echo command")
	}

	vt.Mu.Lock()
	if !vt.ChildExited {
		t.Error("expected child to be exited")
	}
	vt.Mu.Unlock()
}

func TestVirtualTerminal_WritePTY(t *testing.T) {
	vt := &VirtualTerminal{}

	// Start cat which reads stdin and echoes to stdout
	err := vt.StartPTY("/bin/cat", nil, 24, 80, nil)
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}

	var received []byte
	var mu sync.Mutex

	go func() {
		_ = vt.PipeOutput(func(data []byte) {
			mu.Lock()
			received = append(received, data...)
			mu.Unlock()
		})
	}()

	// Write to PTY
	n, err := vt.WritePTY([]byte("test\n"), 3*time.Second)
	if err != nil {
		t.Fatalf("WritePTY: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}

	// Give it time to echo back
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if len(received) == 0 {
		t.Error("expected to receive echoed data")
	}
	mu.Unlock()

	// Clean up
	vt.KillChild()
	vt.Close()
}

func TestVirtualTerminal_WritePTYTimeout(t *testing.T) {
	vt := &VirtualTerminal{}

	// Start sleep which doesn't read stdin
	err := vt.StartPTY("/bin/sleep", []string{"30"}, 24, 80, nil)
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}

	go func() {
		_ = vt.PipeOutput(func(data []byte) {})
	}()

	// Fill up the PTY buffer to cause writes to block.
	// We'll write a large amount of data to fill the kernel buffer.
	bigData := make([]byte, 1024*1024) // 1MB
	for i := range bigData {
		bigData[i] = 'A'
	}

	// Try to write with a very short timeout
	_, err = vt.WritePTY(bigData, 50*time.Millisecond)
	// It may or may not timeout depending on kernel buffer size,
	// but the mechanism should not panic or deadlock
	_ = err

	vt.KillChild()
	vt.Close()
}

func TestVirtualTerminal_Resize(t *testing.T) {
	vt := &VirtualTerminal{}

	err := vt.StartPTY("/bin/sleep", []string{"5"}, 24, 80, nil)
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}

	go func() {
		_ = vt.PipeOutput(func(data []byte) {})
	}()

	// Resize should not panic
	vt.Resize(50, 120, 50)

	vt.Mu.Lock()
	if vt.Rows != 50 || vt.Cols != 120 {
		t.Errorf("expected 50x120, got %dx%d", vt.Rows, vt.Cols)
	}
	vt.Mu.Unlock()

	vt.KillChild()
	vt.Close()
}

func TestVirtualTerminal_KillChild(t *testing.T) {
	vt := &VirtualTerminal{}

	err := vt.StartPTY("/bin/sleep", []string{"300"}, 24, 80, nil)
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = vt.PipeOutput(func(data []byte) {})
		close(done)
	}()

	vt.KillChild()

	select {
	case <-done:
		// Expected
	case <-time.After(5 * time.Second):
		t.Fatal("KillChild did not terminate the child within timeout")
	}

	vt.Mu.Lock()
	if !vt.ChildExited {
		t.Error("expected child to be exited after kill")
	}
	vt.Mu.Unlock()

	vt.Close()
}

func TestVirtualTerminal_EnvVars(t *testing.T) {
	vt := &VirtualTerminal{}

	env := map[string]string{
		"TEST_VAR": "hello_world",
	}

	err := vt.StartPTY("/bin/sh", []string{"-c", "echo $TEST_VAR"}, 24, 80, env)
	if err != nil {
		t.Fatalf("StartPTY: %v", err)
	}

	var got []byte
	var mu sync.Mutex
	_ = vt.PipeOutput(func(data []byte) {
		mu.Lock()
		got = append(got, data...)
		mu.Unlock()
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Error("expected to receive env var output")
	}
}

func TestVirtualTerminal_DoubleClose(t *testing.T) {
	vt := &VirtualTerminal{}
	// Close without starting should not panic
	err := vt.Close()
	if err != nil {
		t.Errorf("Close on unstarted VT: %v", err)
	}
	// Double close
	err = vt.Close()
	if err != nil {
		t.Errorf("Double close: %v", err)
	}
}
