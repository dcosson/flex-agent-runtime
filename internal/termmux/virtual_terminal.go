package termmux

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

// ErrPTYWriteTimeout is returned when a write to the PTY master times out,
// indicating the child process is likely hung.
var ErrPTYWriteTimeout = errors.New("pty write timed out (child may be hung)")

// VirtualTerminal wraps a PTY and child process with robust lifecycle management.
type VirtualTerminal struct {
	Ptm *os.File // PTY master
	Cmd *exec.Cmd
	Mu  sync.Mutex // Guards all terminal state and writes

	Rows, Cols int
	ChildRows  int

	// Child process state
	ChildExited bool
	ChildHung   bool
	ExitError   error

	// Scrollback for terminal streaming
	scrollback *scrollbackBuffer
}

// NewVirtualTerminal creates a VirtualTerminal with scrollback support.
func NewVirtualTerminal() *VirtualTerminal {
	return &VirtualTerminal{
		scrollback: newScrollbackBuffer(maxScrollbackBytes),
	}
}

// AppendScrollback adds raw PTY output to the scrollback buffer.
// This is called from PipeOutput's callback to record output for
// late-attaching terminal subscribers.
func (vt *VirtualTerminal) AppendScrollback(data []byte) {
	vt.Mu.Lock()
	defer vt.Mu.Unlock()
	vt.scrollback.Write(data)
}

// ScrollbackSnapshot returns a copy of the most recent scrollback history
// as raw bytes suitable for replay into xterm.js.
// The snapshot is capped at MaxScrollbackSnapshotBytes.
// Must be called with Mu held.
func (vt *VirtualTerminal) ScrollbackSnapshot() []byte {
	return vt.scrollback.Snapshot(MaxScrollbackSnapshotBytes)
}

// StartPTY allocates a PTY and starts the command in it.
// If cwd is non-empty, the child process starts in that directory.
func (vt *VirtualTerminal) StartPTY(command string, args []string, rows, cols int, env map[string]string, cwd string) error {
	ptm, pts, err := openPTY()
	if err != nil {
		return fmt.Errorf("open pty: %w", err)
	}

	// Set initial size
	if err := setPTYSize(ptm, rows, cols); err != nil {
		ptm.Close()
		pts.Close()
		return fmt.Errorf("set pty size: %w", err)
	}

	cmd := exec.Command(command, args...)
	cmd.Stdin = pts
	cmd.Stdout = pts
	cmd.Stderr = pts
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Build environment: system → terminal defaults → user overrides
	cmdEnv := os.Environ()
	for k, v := range TerminalEnvDefaults() {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	for k, v := range env {
		cmdEnv = append(cmdEnv, k+"="+v)
	}
	cmd.Env = cmdEnv

	if err := cmd.Start(); err != nil {
		ptm.Close()
		pts.Close()
		return fmt.Errorf("start command: %w", err)
	}

	// Close slave side — child has it via the fork
	pts.Close()

	vt.Mu.Lock()
	vt.Ptm = ptm
	vt.Cmd = cmd
	vt.Rows = rows
	vt.Cols = cols
	vt.ChildRows = rows
	vt.ChildExited = false
	vt.ChildHung = false
	vt.ExitError = nil
	vt.Mu.Unlock()

	return nil
}

// PipeOutput reads from the PTY master and calls onData for each chunk.
// This blocks until the child exits or the PTY is closed. Runs its own
// goroutine to wait on the child process.
func (vt *VirtualTerminal) PipeOutput(onData func([]byte)) error {
	// Start goroutine to wait for child exit
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in PipeOutput wait goroutine: %v\n%s\n", r, debug.Stack())
			}
		}()

		err := vt.Cmd.Wait()

		vt.Mu.Lock()
		vt.ChildExited = true
		vt.ExitError = err
		vt.Mu.Unlock()
	}()

	buf := make([]byte, 32*1024)
	for {
		n, err := vt.Ptm.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			safeCallback(func() { onData(data) })
		}
		if err != nil {
			// Wait for child to finish if we haven't already
			<-waitDone
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

// WritePTY writes to the PTY master with a timeout. If the write doesn't
// complete within the timeout, the child is assumed to be hung.
func (vt *VirtualTerminal) WritePTY(p []byte, timeout time.Duration) (int, error) {
	type writeResult struct {
		n   int
		err error
	}

	ch := make(chan writeResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in WritePTY goroutine: %v\n%s\n", r, debug.Stack())
				ch <- writeResult{0, fmt.Errorf("panic in PTY write: %v", r)}
			}
		}()
		n, err := vt.Ptm.Write(p)
		ch <- writeResult{n, err}
	}()

	select {
	case res := <-ch:
		return res.n, res.err
	case <-time.After(timeout):
		return 0, ErrPTYWriteTimeout
	}
}

// KillChild sends SIGKILL to the child process.
func (vt *VirtualTerminal) KillChild() {
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in KillChild: %v\n%s\n", r, debug.Stack())
			}
		}()
		vt.Mu.Lock()
		defer vt.Mu.Unlock()

		if vt.Cmd != nil && vt.Cmd.Process != nil && !vt.ChildExited {
			_ = vt.Cmd.Process.Kill()
		}
	}()
}

// SendSignal sends a signal to the child process.
func (vt *VirtualTerminal) SendSignal(sig syscall.Signal) error {
	vt.Mu.Lock()
	defer vt.Mu.Unlock()

	if vt.Cmd == nil || vt.Cmd.Process == nil || vt.ChildExited {
		return fmt.Errorf("no running child process")
	}
	return vt.Cmd.Process.Signal(sig)
}

// Resize changes the PTY window size.
func (vt *VirtualTerminal) Resize(rows, cols, childRows int) {
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in Resize: %v\n%s\n", r, debug.Stack())
			}
		}()
		vt.Mu.Lock()
		defer vt.Mu.Unlock()

		vt.Rows = rows
		vt.Cols = cols
		vt.ChildRows = childRows
		if vt.Ptm != nil {
			_ = setPTYSize(vt.Ptm, childRows, cols)
		}
	}()
}

// Close closes the PTY master file descriptor.
func (vt *VirtualTerminal) Close() error {
	vt.Mu.Lock()
	defer vt.Mu.Unlock()

	if vt.Ptm != nil {
		return vt.Ptm.Close()
	}
	return nil
}

// safeCallback calls fn with panic recovery.
func safeCallback(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic recovered in callback: %v\n%s\n", r, debug.Stack())
		}
	}()
	fn()
}
