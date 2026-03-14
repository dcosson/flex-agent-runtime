package zfs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

type fakeRunResult struct {
	stdout []byte
	stderr []byte
	exit   int
	err    error
}

func testManager(t *testing.T, fn func(binary string, args ...string) fakeRunResult) *CLIManager {
	t.Helper()
	return &CLIManager{
		zfsPath:         "zfs",
		zpoolPath:       "zpool",
		logger:          slog.Default(),
		defaultTimeout:  defaultCommandTimeout,
		metadataTimeout: metadataCommandTimeout,
		transferTimeout: transferCommandTimeout,
		run: func(_ context.Context, binary string, args ...string) ([]byte, []byte, int, error) {
			res := fn(binary, args...)
			return res.stdout, res.stderr, res.exit, res.err
		},
	}
}

func TestCreateDatasetBuildsCommand(t *testing.T) {
	t.Parallel()
	var gotBinary string
	var gotArgs []string
	m := testManager(t, func(binary string, args ...string) fakeRunResult {
		gotBinary = binary
		gotArgs = append([]string{}, args...)
		return fakeRunResult{}
	})

	err := m.CreateDataset(context.Background(), "pool/sessions/s1", DatasetOptions{
		Mountpoint: "/mnt/s1",
		Quota:      1024,
		Properties: map[string]string{"com.example:foo": "bar"},
	})
	if err != nil {
		t.Fatalf("CreateDataset error = %v", err)
	}
	if gotBinary != "zfs" {
		t.Fatalf("binary = %q, want zfs", gotBinary)
	}
	cmd := strings.Join(gotArgs, " ")
	for _, want := range []string{"create", "mountpoint=/mnt/s1", "quota=1024", "com.example:foo=bar", "pool/sessions/s1"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command %q missing %q", cmd, want)
		}
	}
}

func TestGetDatasetInfoParses(t *testing.T) {
	t.Parallel()
	m := testManager(t, func(binary string, args ...string) fakeRunResult {
		if binary != "zfs" {
			return fakeRunResult{err: fmt.Errorf("bad binary")}
		}
		if len(args) < 1 || args[0] != "get" {
			return fakeRunResult{err: fmt.Errorf("bad args")}
		}
		return fakeRunResult{stdout: []byte("/mnt/s1\n10\n100\n8\n-\n1700000000\n")}
	})

	info, err := m.GetDatasetInfo(context.Background(), "pool/sessions/s1")
	if err != nil {
		t.Fatalf("GetDatasetInfo error = %v", err)
	}
	if info.Name != "pool/sessions/s1" || info.Mountpoint != "/mnt/s1" || info.Used != 10 || info.Available != 100 || info.Referenced != 8 {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestDatasetExistsMapsNotFound(t *testing.T) {
	t.Parallel()
	m := testManager(t, func(binary string, args ...string) fakeRunResult {
		return fakeRunResult{
			stderr: []byte("dataset does not exist"),
			exit:   1,
			err:    errors.New("exit status 1"),
		}
	})
	exists, err := m.DatasetExists(context.Background(), "pool/sessions/missing")
	if err != nil {
		t.Fatalf("DatasetExists error = %v", err)
	}
	if exists {
		t.Fatalf("expected exists=false")
	}
}

func TestListDatasetsParses(t *testing.T) {
	t.Parallel()
	m := testManager(t, func(binary string, args ...string) fakeRunResult {
		return fakeRunResult{stdout: []byte(
			"pool/sessions\t/mnt/sessions\t10\t100\t8\t-\t1700000000\n" +
				"pool/sessions/s1\t/mnt/sessions/s1\t11\t99\t9\tpool/base@init\t1700000001\n",
		)}
	})
	items, err := m.ListDatasets(context.Background(), "pool/sessions")
	if err != nil {
		t.Fatalf("ListDatasets error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
	}
	if items[0].Origin != "" || items[1].Origin != "pool/base@init" {
		t.Fatalf("unexpected origins: %+v", items)
	}
}
