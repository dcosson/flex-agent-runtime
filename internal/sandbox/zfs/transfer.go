package zfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func (m *CLIManager) EstimateSendSize(ctx context.Context, snapshot string, opts SendOptions) (int64, error) {
	if err := ValidateSnapshotFullName(snapshot); err != nil {
		return 0, err
	}
	if opts.Incremental != "" {
		if err := ValidateSnapshotFullName(opts.Incremental); err != nil {
			return 0, err
		}
	}
	args := buildSendArgs(snapshot, opts)
	args = append(args[:1], append([]string{"-n", "-P"}, args[1:]...)...)
	stderr, err := m.execStream(ctx, "EstimateSendSize", args, nil, io.Discard)
	if err != nil {
		return 0, err
	}
	return parseEstimatedSize(string(stderr))
}

func (m *CLIManager) Send(ctx context.Context, snapshot string, opts SendOptions, w io.Writer) error {
	if w == nil {
		return fmt.Errorf("writer is nil")
	}
	if err := ValidateSnapshotFullName(snapshot); err != nil {
		return err
	}
	if opts.Incremental != "" {
		if err := ValidateSnapshotFullName(opts.Incremental); err != nil {
			return err
		}
	}
	_, err := m.execStream(ctx, "Send", buildSendArgs(snapshot, opts), nil, w)
	return err
}

func (m *CLIManager) Receive(ctx context.Context, dataset string, r io.Reader) error {
	if r == nil {
		return fmt.Errorf("reader is nil")
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	_, err := m.execStream(ctx, "Receive", []string{"receive", "-F", dataset}, r, io.Discard)
	return err
}

func buildSendArgs(snapshot string, opts SendOptions) []string {
	args := []string{"send"}
	if opts.Raw {
		args = append(args, "-w")
	}
	if opts.Compressed {
		args = append(args, "-c")
	}
	if opts.LargeBlocks {
		args = append(args, "-L")
	}
	if opts.Incremental != "" {
		args = append(args, "-i", opts.Incremental)
	}
	args = append(args, snapshot)
	return args
}

func parseEstimatedSize(stderr string) (int64, error) {
	for _, line := range strings.Split(stderr, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "size") {
			parts := strings.Fields(trim)
			if len(parts) == 2 {
				v, err := strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return 0, fmt.Errorf("parse estimate %q: %w", trim, err)
				}
				return v, nil
			}
		}
	}
	return 0, fmt.Errorf("no size line found in output: %q", bytes.TrimSpace([]byte(stderr)))
}
