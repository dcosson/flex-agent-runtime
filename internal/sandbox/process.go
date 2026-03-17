package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/flex-agent-runtime/internal/sandbox/gvisor"
)

const exitedProcessRetention = 10 * time.Minute

type ManagedProcess struct {
	mu sync.RWMutex

	id       string
	status   ProcessStatus
	exitCode *int
	pid      int

	cmd        *exec.Cmd
	cancel     context.CancelFunc
	killSignal int

	// gVisor launch path.
	gvisorCtx  context.Context
	gvisorOpts gvisor.ContainerOptions

	proxy *portProxy

	startedAt time.Time
	exitedAt  time.Time
	done      chan struct{}
}

type portProxy struct {
	address    string
	targetAddr string
	listener   net.Listener
	done       chan struct{}

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func (svc *SandboxHostService) LaunchProcess(ctx context.Context, req LaunchProcessRequest) (*LaunchProcessResponse, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if req.Binary == "" {
		return nil, fmt.Errorf("binary is required")
	}
	if req.ExposePort < 0 || req.ExposePort > 65535 {
		return nil, fmt.Errorf("expose_port must be in range [0,65535]")
	}

	sess, err := svc.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}

	sess.mu.Lock()
	if sess.state != SessionActive {
		sess.mu.Unlock()
		return nil, fmt.Errorf("%w: session is %s", ErrInvalidState, sess.state)
	}
	if sess.processes == nil {
		sess.processes = make(map[string]*ManagedProcess)
	}
	svc.pruneExitedProcessesLocked(sess, time.Now())
	sess.processSeq++
	processID := fmt.Sprintf("proc-%s-%d", shortSessionID(req.SessionID), sess.processSeq)
	mountpoint := sess.mountpoint
	proc := &ManagedProcess{
		id:        processID,
		status:    ProcessStatusStarting,
		startedAt: time.Now(),
		done:      make(chan struct{}),
	}
	sess.processes[processID] = proc
	sess.mu.Unlock()

	var startErr error
	if svc.config.ContainerRuntime == ContainerRuntimeGVisor {
		startErr = svc.launchInGVisor(proc, req, mountpoint)
	} else {
		startErr = svc.launchDirect(proc, req, mountpoint)
	}
	if startErr != nil {
		sess.mu.Lock()
		delete(sess.processes, processID)
		sess.mu.Unlock()
		return nil, fmt.Errorf("launch process: %w", startErr)
	}

	if req.ExposePort > 0 {
		proxy, err := svc.createPortProxy(req.ExposePort)
		if err != nil {
			_ = svc.killManagedProcess(proc, syscall.SIGKILL)
			sess.mu.Lock()
			delete(sess.processes, processID)
			sess.mu.Unlock()
			return nil, fmt.Errorf("setup port proxy: %w", err)
		}
		proc.mu.Lock()
		proc.proxy = proxy
		proc.mu.Unlock()
	}

	go svc.monitorProcess(sess, proc)

	proc.mu.RLock()
	status := proc.status
	address := ""
	if proc.proxy != nil {
		address = proc.proxy.address
	}
	proc.mu.RUnlock()

	return &LaunchProcessResponse{
		ProcessID: processID,
		Address:   address,
		Status:    status,
	}, nil
}

func (svc *SandboxHostService) KillProcess(_ context.Context, req KillProcessRequest) error {
	if req.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if req.ProcessID == "" {
		return fmt.Errorf("process_id is required")
	}

	sess, err := svc.getSession(req.SessionID)
	if err != nil {
		return err
	}

	sess.mu.RLock()
	proc, ok := sess.processes[req.ProcessID]
	sess.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrProcessNotFound, req.ProcessID)
	}

	sig := syscall.Signal(req.Signal)
	if sig == 0 {
		sig = syscall.SIGTERM
	}
	return svc.killManagedProcess(proc, sig)
}

func (svc *SandboxHostService) GetProcessStatus(_ context.Context, req GetProcessStatusRequest) (*GetProcessStatusResponse, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if req.ProcessID == "" {
		return nil, fmt.Errorf("process_id is required")
	}

	sess, err := svc.getSession(req.SessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.RLock()
	proc, ok := sess.processes[req.ProcessID]
	sess.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrProcessNotFound, req.ProcessID)
	}
	proc.mu.RLock()
	defer proc.mu.RUnlock()
	var exitCode *int
	if proc.exitCode != nil {
		code := *proc.exitCode
		exitCode = &code
	}
	return &GetProcessStatusResponse{Status: proc.status, ExitCode: exitCode}, nil
}

func (svc *SandboxHostService) launchDirect(proc *ManagedProcess, req LaunchProcessRequest, mountpoint string) error {
	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, req.Binary, req.Args...)
	cmd.Dir = mountpoint
	cmd.Env = buildProcessEnv(req.Env)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}

	proc.mu.Lock()
	proc.cmd = cmd
	proc.cancel = cancel
	proc.pid = cmd.Process.Pid
	proc.status = ProcessStatusRunning
	proc.mu.Unlock()
	return nil
}

func (svc *SandboxHostService) launchInGVisor(proc *ManagedProcess, req LaunchProcessRequest, mountpoint string) error {
	if svc.gvisor == nil {
		return fmt.Errorf("gvisor runtime requested but manager is nil")
	}
	procCtx, cancel := context.WithCancel(context.Background())
	network := gvisor.NetworkNone
	if req.ExposePort > 0 {
		network = gvisor.NetworkHost
	}
	opts := gvisor.ContainerOptions{
		Command:   append([]string{req.Binary}, req.Args...),
		WorkDir:   "/workspace",
		Env:       req.Env,
		RootFS:    mountpoint,
		Resources: svc.config.DefaultResources,
		Network:   network,
	}

	proc.mu.Lock()
	proc.cancel = cancel
	proc.gvisorCtx = procCtx
	proc.gvisorOpts = opts
	proc.status = ProcessStatusRunning
	proc.mu.Unlock()
	return nil
}

func (svc *SandboxHostService) monitorProcess(sess *Session, proc *ManagedProcess) {
	defer close(proc.done)
	proc.mu.RLock()
	cmd := proc.cmd
	gvisorCtx := proc.gvisorCtx
	gvisorOpts := proc.gvisorOpts
	proc.mu.RUnlock()

	if cmd != nil {
		svc.monitorDirectProcess(sess, proc, cmd)
		return
	}
	if gvisorCtx != nil && len(gvisorOpts.Command) > 0 {
		svc.monitorGVisorProcess(sess, proc, gvisorCtx, gvisorOpts)
	}
}

func (svc *SandboxHostService) monitorDirectProcess(sess *Session, proc *ManagedProcess, cmd *exec.Cmd) {
	err := cmd.Wait()
	exit := 0
	switch {
	case err == nil:
		exit = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		} else {
			exit = -1
		}
	}
	svc.markProcessExited(sess, proc, exit)
}

func (svc *SandboxHostService) monitorGVisorProcess(sess *Session, proc *ManagedProcess, runCtx context.Context, opts gvisor.ContainerOptions) {
	result, err := svc.gvisor.Run(runCtx, opts)
	exit := 0
	switch {
	case err == nil && result != nil:
		exit = result.ExitCode
	case err == nil:
		exit = 0
	case errors.Is(err, context.Canceled):
		proc.mu.RLock()
		sig := proc.killSignal
		proc.mu.RUnlock()
		if sig == 0 {
			sig = int(syscall.SIGTERM)
		}
		exit = 128 + sig
	default:
		exit = -1
	}
	svc.markProcessExited(sess, proc, exit)
}

func (svc *SandboxHostService) markProcessExited(sess *Session, proc *ManagedProcess, exitCode int) {
	proc.mu.Lock()
	if proc.status == ProcessStatusExited {
		proc.mu.Unlock()
		return
	}
	proc.status = ProcessStatusExited
	code := exitCode
	proc.exitCode = &code
	proc.exitedAt = time.Now()
	proxy := proc.proxy
	proc.mu.Unlock()

	if proxy != nil {
		proxy.close()
	}

	svc.logger.Info(
		"managed process exited",
		"session_id", sess.id,
		"process_id", proc.id,
		"exit_code", exitCode,
	)
}

func (svc *SandboxHostService) killManagedProcess(proc *ManagedProcess, sig syscall.Signal) error {
	proc.mu.Lock()
	if proc.status == ProcessStatusExited {
		proc.mu.Unlock()
		return nil
	}
	proc.killSignal = int(sig)
	cmd := proc.cmd
	cancel := proc.cancel
	proc.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		err := cmd.Process.Signal(sig)
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

func (svc *SandboxHostService) destroySessionProcesses(ctx context.Context, sess *Session) {
	sess.mu.RLock()
	if len(sess.processes) == 0 {
		sess.mu.RUnlock()
		return
	}
	processes := make([]*ManagedProcess, 0, len(sess.processes))
	for _, proc := range sess.processes {
		processes = append(processes, proc)
	}
	sess.mu.RUnlock()

	for _, proc := range processes {
		_ = svc.killManagedProcess(proc, syscall.SIGTERM)
	}
	for _, proc := range processes {
		select {
		case <-proc.done:
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
			_ = svc.killManagedProcess(proc, syscall.SIGKILL)
			select {
			case <-proc.done:
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
		proc.mu.RLock()
		proxy := proc.proxy
		proc.mu.RUnlock()
		if proxy != nil {
			proxy.close()
		}
	}

	sess.mu.Lock()
	clear(sess.processes)
	sess.mu.Unlock()
}

func (svc *SandboxHostService) createPortProxy(targetPort int) (*portProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	portStr := ""
	if addr, ok := listener.Addr().(*net.TCPAddr); ok {
		portStr = strconv.Itoa(addr.Port)
	} else {
		_, p, splitErr := net.SplitHostPort(listener.Addr().String())
		if splitErr != nil {
			_ = listener.Close()
			return nil, splitErr
		}
		portStr = p
	}

	host := svc.config.AdvertiseAddr
	if host == "" {
		host = "127.0.0.1"
	}
	proxy := &portProxy{
		address:    net.JoinHostPort(host, portStr),
		targetAddr: net.JoinHostPort("127.0.0.1", strconv.Itoa(targetPort)),
		listener:   listener,
		done:       make(chan struct{}),
		conns:      make(map[net.Conn]struct{}),
	}
	go proxy.serve()
	return proxy, nil
}

func (p *portProxy) serve() {
	defer close(p.done)
	for {
		clientConn, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.trackConn(clientConn)
		go p.handleConn(clientConn)
	}
}

func (p *portProxy) handleConn(clientConn net.Conn) {
	defer func() {
		p.untrackConn(clientConn)
		_ = clientConn.Close()
	}()

	targetConn, err := net.DialTimeout("tcp", p.targetAddr, 5*time.Second)
	if err != nil {
		return
	}
	p.trackConn(targetConn)
	defer func() {
		p.untrackConn(targetConn)
		_ = targetConn.Close()
	}()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(targetConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, targetConn)
		done <- struct{}{}
	}()
	<-done
}

func (p *portProxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	for c := range p.conns {
		_ = c.Close()
	}
	p.mu.Unlock()
	<-p.done
}

func (p *portProxy) trackConn(c net.Conn) {
	p.mu.Lock()
	p.conns[c] = struct{}{}
	p.mu.Unlock()
}

func (p *portProxy) untrackConn(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
}

func buildProcessEnv(env map[string]string) []string {
	base := os.Environ()
	if len(env) == 0 {
		return base
	}
	out := append([]string{}, base...)
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func shortSessionID(sessionID string) string {
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8]
}

func (svc *SandboxHostService) pruneExitedProcessesLocked(sess *Session, now time.Time) {
	if len(sess.processes) == 0 {
		return
	}
	for id, proc := range sess.processes {
		proc.mu.RLock()
		exited := proc.status == ProcessStatusExited
		exitedAt := proc.exitedAt
		proc.mu.RUnlock()
		if exited && !exitedAt.IsZero() && now.Sub(exitedAt) > exitedProcessRetention {
			delete(sess.processes, id)
		}
	}
}
