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
	"unsafe"
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
}

// StartPTY allocates a PTY and starts the command in it.
func (vt *VirtualTerminal) StartPTY(command string, args []string, rows, cols int, env map[string]string) error {
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

	// Build environment
	cmdEnv := os.Environ()
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

// openPTY opens a new PTY master/slave pair.
func openPTY() (master, slave *os.File, err error) {
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	name, err := ptsname(ptm)
	if err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("ptsname: %w", err)
	}

	if err := grantpt(ptm); err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("grantpt: %w", err)
	}

	if err := unlockpt(ptm); err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("unlockpt: %w", err)
	}

	pts, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		ptm.Close()
		return nil, nil, fmt.Errorf("open pts: %w", err)
	}

	return ptm, pts, nil
}

// ptsname returns the name of the slave PTY.
func ptsname(f *os.File) (string, error) {
	// On Darwin, use TIOCPTYGNAME ioctl
	var buf [128]byte
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return "", errno
	}
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i]), nil
		}
	}
	return string(buf[:]), nil
}

func grantpt(f *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCPTYGRANT, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func unlockpt(f *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCPTYUNLK, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// setPTYSize sets the window size of the PTY.
func setPTYSize(f *os.File, rows, cols int) error {
	ws := struct {
		Row    uint16
		Col    uint16
		Xpixel uint16
		Ypixel uint16
	}{
		Row: uint16(rows),
		Col: uint16(cols),
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return errno
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
