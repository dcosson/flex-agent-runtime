package zfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCommandTimeout  = 30 * time.Second
	metadataCommandTimeout = 30 * time.Second
	transferCommandTimeout = 10 * time.Minute
)

type commandRunner func(ctx context.Context, binary string, args ...string) ([]byte, []byte, int, error)
type streamRunner func(ctx context.Context, binary string, args []string, stdin io.Reader, stdout io.Writer) ([]byte, int, error)

// CLIManager implements ZFSManager by shelling out to zfs/zpool commands.
type CLIManager struct {
	zfsPath   string
	zpoolPath string
	sudo      bool
	logger    *slog.Logger
	pool      string

	defaultTimeout  time.Duration
	metadataTimeout time.Duration
	transferTimeout time.Duration

	run       commandRunner
	runStream streamRunner
}

type CLIOption func(*CLIManager)

func WithZFSPath(path string) CLIOption {
	return func(m *CLIManager) { m.zfsPath = path }
}

func WithZPoolPath(path string) CLIOption {
	return func(m *CLIManager) { m.zpoolPath = path }
}

func WithSudo(sudo bool) CLIOption {
	return func(m *CLIManager) { m.sudo = sudo }
}

func WithLogger(logger *slog.Logger) CLIOption {
	return func(m *CLIManager) {
		if logger != nil {
			m.logger = logger
		}
	}
}

func WithPool(pool string) CLIOption {
	return func(m *CLIManager) { m.pool = pool }
}

func WithDefaultCommandTimeout(timeout time.Duration) CLIOption {
	return func(m *CLIManager) {
		if timeout > 0 {
			m.defaultTimeout = timeout
		}
	}
}

func WithMetadataCommandTimeout(timeout time.Duration) CLIOption {
	return func(m *CLIManager) {
		if timeout > 0 {
			m.metadataTimeout = timeout
		}
	}
}

func WithTransferCommandTimeout(timeout time.Duration) CLIOption {
	return func(m *CLIManager) {
		if timeout > 0 {
			m.transferTimeout = timeout
		}
	}
}

func NewCLIManager(opts ...CLIOption) (*CLIManager, error) {
	m := &CLIManager{
		zfsPath:         "zfs",
		zpoolPath:       "zpool",
		logger:          slog.Default(),
		defaultTimeout:  defaultCommandTimeout,
		metadataTimeout: metadataCommandTimeout,
		transferTimeout: transferCommandTimeout,
		run:             defaultRunner,
		runStream:       defaultStreamRunner,
	}
	for _, opt := range opts {
		opt(m)
	}
	if _, err := exec.LookPath(m.zfsPath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandNotFound, err)
	}
	if _, err := exec.LookPath(m.zpoolPath); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCommandNotFound, err)
	}
	return m, nil
}

func defaultRunner(ctx context.Context, binary string, args ...string) ([]byte, []byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return stdout.Bytes(), stderr.Bytes(), exitCode, err
}

func defaultStreamRunner(ctx context.Context, binary string, args []string, stdin io.Reader, stdout io.Writer) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	return stderr.Bytes(), exitCode, err
}

func (m *CLIManager) timeoutFor(op string) time.Duration {
	switch op {
	case "EstimateSendSize", "Send", "Receive":
		return m.transferTimeout
	default:
		return m.metadataTimeout
	}
}

func (m *CLIManager) command(binary string, args []string) (string, []string) {
	if m.sudo {
		return "sudo", append([]string{binary}, args...)
	}
	return binary, args
}

func (m *CLIManager) exec(ctx context.Context, op string, args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, &ZFSError{Op: op, Err: fmt.Errorf("missing command args")}
	}
	binary := m.zfsPath
	cmdArgs := args
	if args[0] == "zpool" {
		binary = m.zpoolPath
		cmdArgs = args[1:]
	}
	binary, cmdArgs = m.command(binary, cmdArgs)

	execCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, m.timeoutFor(op))
		defer cancel()
	}

	start := time.Now()
	stdout, stderr, exitCode, err := m.run(execCtx, binary, cmdArgs...)
	dur := time.Since(start)
	stderrStr := strings.TrimSpace(string(stderr))
	m.logger.DebugContext(execCtx, "zfs command",
		"op", op,
		"binary", binary,
		"args", strings.Join(cmdArgs, " "),
		"duration", dur,
		"exit_code", exitCode,
		"stderr", stderrStr,
	)
	if err != nil {
		wrapped := classifyError(stderrStr, exitCode)
		if ctxErr := execCtx.Err(); ctxErr != nil {
			wrapped = ctxErr
		}
		return nil, &ZFSError{
			Op:       op,
			Command:  binary + " " + strings.Join(cmdArgs, " "),
			ExitCode: exitCode,
			Stderr:   stderrStr,
			Err:      wrapped,
		}
	}
	return stdout, nil
}

func (m *CLIManager) execStream(ctx context.Context, op string, args []string, stdin io.Reader, stdout io.Writer) ([]byte, error) {
	if len(args) == 0 {
		return nil, &ZFSError{Op: op, Err: fmt.Errorf("missing command args")}
	}
	binary := m.zfsPath
	cmdArgs := args
	if args[0] == "zpool" {
		binary = m.zpoolPath
		cmdArgs = args[1:]
	}
	binary, cmdArgs = m.command(binary, cmdArgs)

	execCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, m.timeoutFor(op))
		defer cancel()
	}

	start := time.Now()
	stderr, exitCode, err := m.runStream(execCtx, binary, cmdArgs, stdin, stdout)
	dur := time.Since(start)
	stderrStr := strings.TrimSpace(string(stderr))
	m.logger.DebugContext(execCtx, "zfs stream command",
		"op", op,
		"binary", binary,
		"args", strings.Join(cmdArgs, " "),
		"duration", dur,
		"exit_code", exitCode,
		"stderr", stderrStr,
	)
	if err != nil {
		wrapped := classifyError(stderrStr, exitCode)
		if ctxErr := execCtx.Err(); ctxErr != nil {
			wrapped = ctxErr
		}
		return stderr, &ZFSError{
			Op:       op,
			Command:  binary + " " + strings.Join(cmdArgs, " "),
			ExitCode: exitCode,
			Stderr:   stderrStr,
			Err:      wrapped,
		}
	}
	return stderr, nil
}

func parseTabular(output []byte, expectedCols int) ([][]string, error) {
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, nil
	}
	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != expectedCols {
			return nil, fmt.Errorf("expected %d columns, got %d in %q", expectedCols, len(cols), line)
		}
		rows = append(rows, cols)
	}
	return rows, nil
}

func parseSize(s string) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", s, err)
	}
	return v, nil
}

func parseTimestamp(s string) (time.Time, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return time.Unix(v, 0).UTC(), nil
}

func parseRatio(s string) (float64, error) {
	raw := strings.TrimSpace(strings.TrimSuffix(s, "%"))
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ratio %q: %w", s, err)
	}
	if v > 1 {
		return v / 100, nil
	}
	return v, nil
}

func parsePoolStatus(pool string, out []byte) (*PoolStatus, error) {
	status := &PoolStatus{Name: pool}
	for _, line := range strings.Split(string(out), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "state:"):
			status.State = PoolState(strings.TrimSpace(strings.TrimPrefix(trim, "state:")))
		case strings.HasPrefix(trim, "scan:"):
			status.Scan = strings.TrimSpace(strings.TrimPrefix(trim, "scan:"))
		case strings.HasPrefix(trim, "errors:"):
			status.Errors = strings.TrimSpace(strings.TrimPrefix(trim, "errors:"))
		}
	}
	if status.State == "" {
		return nil, fmt.Errorf("pool status parse: missing state")
	}
	return status, nil
}

func parsePoolSpace(out []byte) (*PoolSpace, error) {
	rows, err := parseTabular(out, 6)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("expected exactly one pool row, got %d", len(rows))
	}
	row := rows[0]
	sz, err := parseSize(row[1])
	if err != nil {
		return nil, err
	}
	alloc, err := parseSize(row[2])
	if err != nil {
		return nil, err
	}
	free, err := parseSize(row[3])
	if err != nil {
		return nil, err
	}
	capRatio, err := parseRatio(row[4])
	if err != nil {
		return nil, err
	}
	fragRatio, err := parseRatio(row[5])
	if err != nil {
		return nil, err
	}
	return &PoolSpace{
		Pool:          row[0],
		Size:          sz,
		Allocated:     alloc,
		Free:          free,
		Capacity:      capRatio,
		Fragmentation: fragRatio,
	}, nil
}
