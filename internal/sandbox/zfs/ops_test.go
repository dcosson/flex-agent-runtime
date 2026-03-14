package zfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type fakeStreamResult struct {
	stderr []byte
	exit   int
	err    error
}

func testManagerWithStream(t *testing.T, runFn func(binary string, args ...string) fakeRunResult, streamFn func(binary string, args []string, stdin io.Reader, stdout io.Writer) fakeStreamResult) *CLIManager {
	t.Helper()
	if runFn == nil {
		runFn = func(_ string, _ ...string) fakeRunResult { return fakeRunResult{} }
	}
	if streamFn == nil {
		streamFn = func(_ string, _ []string, _ io.Reader, _ io.Writer) fakeStreamResult { return fakeStreamResult{} }
	}
	return &CLIManager{
		zfsPath:         "zfs",
		zpoolPath:       "zpool",
		logger:          slog.Default(),
		defaultTimeout:  defaultCommandTimeout,
		metadataTimeout: metadataCommandTimeout,
		transferTimeout: transferCommandTimeout,
		run: func(_ context.Context, binary string, args ...string) ([]byte, []byte, int, error) {
			res := runFn(binary, args...)
			return res.stdout, res.stderr, res.exit, res.err
		},
		runStream: func(_ context.Context, binary string, args []string, stdin io.Reader, stdout io.Writer) ([]byte, int, error) {
			res := streamFn(binary, args, stdin, stdout)
			return res.stderr, res.exit, res.err
		},
	}
}

func TestCreateSnapshotBuildsAndFetchesInfo(t *testing.T) {
	t.Parallel()
	calls := 0
	m := testManagerWithStream(t, func(_ string, args ...string) fakeRunResult {
		calls++
		if calls == 1 {
			if strings.Join(args, " ") != "snapshot pool/sessions/s1@turn-1" {
				t.Fatalf("unexpected first call: %v", args)
			}
			return fakeRunResult{}
		}
		return fakeRunResult{stdout: []byte("pool/sessions/s1@turn-1\t1\t2\t1700000000\t0\n")}
	}, nil)

	info, err := m.CreateSnapshot(context.Background(), "pool/sessions/s1", "turn-1")
	if err != nil {
		t.Fatalf("CreateSnapshot error = %v", err)
	}
	if info.Name != "turn-1" || info.Dataset != "pool/sessions/s1" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestRollbackDestroyLaterFlag(t *testing.T) {
	t.Parallel()
	m := testManagerWithStream(t, func(_ string, args ...string) fakeRunResult {
		got := strings.Join(args, " ")
		if got != "rollback -r pool/sessions/s1@turn-1" {
			t.Fatalf("unexpected command: %s", got)
		}
		return fakeRunResult{}
	}, nil)
	if err := m.Rollback(context.Background(), "pool/sessions/s1", "turn-1", RollbackOptions{DestroyLater: true}); err != nil {
		t.Fatal(err)
	}
}

func TestListSnapshotsParses(t *testing.T) {
	t.Parallel()
	m := testManagerWithStream(t, func(_ string, _ ...string) fakeRunResult {
		return fakeRunResult{stdout: []byte("pool/sessions/s1@turn-1\t1\t2\t1700000000\t0\npool/sessions/s1@turn-2\t2\t3\t1700000001\t1\n")}
	}, nil)

	items, err := m.ListSnapshots(context.Background(), "pool/sessions/s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Name != "turn-2" || items[1].Holds != 1 {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestPoolOps(t *testing.T) {
	t.Parallel()
	m := testManagerWithStream(t, func(binary string, args ...string) fakeRunResult {
		cmd := strings.Join(append([]string{binary}, args...), " ")
		switch {
		case strings.Contains(cmd, "zpool status"):
			return fakeRunResult{stdout: []byte("  pool: tank\n state: ONLINE\n  scan: scrub repaired 0B\nerrors: No known data errors\n")}
		case strings.Contains(cmd, "zpool list"):
			return fakeRunResult{stdout: []byte("tank\t100\t20\t80\t20\t5\n")}
		default:
			return fakeRunResult{}
		}
	}, nil)

	st, err := m.PoolStatus(context.Background(), "tank")
	if err != nil || st.State != PoolOnline {
		t.Fatalf("PoolStatus = (%+v, %v)", st, err)
	}
	sp, err := m.PoolSpace(context.Background(), "tank")
	if err != nil || sp.Capacity != 0.2 || sp.Fragmentation != 0.05 {
		t.Fatalf("PoolSpace = (%+v, %v)", sp, err)
	}
}

func TestImportExportCommands(t *testing.T) {
	t.Parallel()
	calls := []string{}
	m := testManagerWithStream(t, func(binary string, args ...string) fakeRunResult {
		calls = append(calls, strings.Join(append([]string{binary}, args...), " "))
		return fakeRunResult{}
	}, nil)
	if err := m.ImportPool(context.Background(), "tank", "/dev/disk/by-id/xyz"); err != nil {
		t.Fatal(err)
	}
	if err := m.ExportPool(context.Background(), "tank"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[0], "zpool import") || !strings.Contains(calls[1], "zpool export") {
		t.Fatalf("unexpected calls: %#v", calls)
	}
}

func TestTransferOps(t *testing.T) {
	t.Parallel()
	m := testManagerWithStream(t, nil, func(_ string, args []string, stdin io.Reader, stdout io.Writer) fakeStreamResult {
		if len(args) > 2 && args[0] == "send" && args[1] == "-n" && args[2] == "-P" {
			return fakeStreamResult{stderr: []byte("size 4096\n")}
		}
		if len(args) > 0 && args[0] == "send" {
			_, _ = io.WriteString(stdout, "stream")
			return fakeStreamResult{}
		}
		if len(args) > 0 && args[0] == "receive" {
			_, _ = io.ReadAll(stdin)
			return fakeStreamResult{}
		}
		return fakeStreamResult{err: errors.New("unexpected args")}
	})

	sz, err := m.EstimateSendSize(context.Background(), "pool/base@init", SendOptions{})
	if err != nil || sz != 4096 {
		t.Fatalf("EstimateSendSize = (%d, %v)", sz, err)
	}
	var out bytes.Buffer
	if err := m.Send(context.Background(), "pool/base@init", SendOptions{Compressed: true}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "stream" {
		t.Fatalf("unexpected send output: %q", out.String())
	}
	if err := m.Receive(context.Background(), "pool/target", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
}
