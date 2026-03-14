package zfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"pgregory.net/rapid"
)

func TestP1NameValidationRoundtrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		name := rapid.StringMatching(`[a-zA-Z0-9_./:\-@]{0,200}`).Draw(rt, "name")
		err := ValidateName(name)
		if err == nil {
			if e := ValidateName(name); e != nil {
				rt.Fatalf("expected stable valid result, got %v", e)
			}
		} else if !errors.Is(err, ErrInvalidName) {
			rt.Fatalf("expected ErrInvalidName, got %v", err)
		}
	})
}

func TestP2SnapshotNameNeverContainsShellMetacharacters(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		name := rapid.String().Draw(rt, "name")
		err := ValidateSnapshotName(name)
		if err != nil {
			return
		}
		for _, c := range name {
			if bytes.ContainsRune([]byte("'\"`$;|&\\(){}[]<>!#~ \t\n"), c) {
				rt.Fatalf("unexpected metacharacter %q in %q", c, name)
			}
		}
	})
}

func TestP3ErrorClassificationTotality(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		stderr := rapid.String().Draw(rt, "stderr")
		exitCode := rapid.IntRange(1, 255).Draw(rt, "exit")
		err := classifyError(stderr, exitCode)
		if err == nil {
			rt.Fatalf("expected non-nil classification for exit=%d", exitCode)
		}
	})
}

func TestP4MockManagerConsistency(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ctx := context.Background()
		m := NewMockManager()
		ds := "pool/p4"
		if err := m.CreateDataset(ctx, ds, DatasetOptions{}); err != nil {
			rt.Fatalf("CreateDataset: %v", err)
		}
		snaps := rapid.IntRange(1, 5).Draw(rt, "snaps")
		for i := 0; i < snaps; i++ {
			if _, err := m.CreateSnapshot(ctx, ds, fmt.Sprintf("s-%d", i)); err != nil {
				rt.Fatalf("CreateSnapshot: %v", err)
			}
		}
		list, err := m.ListSnapshots(ctx, ds)
		if err != nil || len(list) != snaps {
			rt.Fatalf("ListSnapshots got len=%d err=%v want=%d", len(list), err, snaps)
		}
		if err := m.Rollback(ctx, ds, "s-0", RollbackOptions{DestroyLater: true}); err != nil {
			rt.Fatalf("Rollback: %v", err)
		}
		list, _ = m.ListSnapshots(ctx, ds)
		if len(list) != 1 {
			rt.Fatalf("expected 1 snapshot after rollback, got %d", len(list))
		}
	})
}

func TestP5ValidateSnapshotFullNameDecomposition(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dataset := rapid.StringMatching(`[a-zA-Z0-9_./:\-]{1,40}`).Draw(rt, "dataset")
		snap := rapid.StringMatching(`[a-zA-Z0-9_.\-]{1,40}`).Draw(rt, "snap")
		fullErr := ValidateSnapshotFullName(dataset + "@" + snap)
		dsErr := ValidateName(dataset)
		sErr := ValidateSnapshotName(snap)
		if dsErr == nil && sErr == nil && fullErr != nil {
			rt.Fatalf("expected valid full name, got %v", fullErr)
		}
		if fullErr == nil && (dsErr != nil || sErr != nil) {
			rt.Fatalf("full valid but parts invalid: ds=%v snap=%v", dsErr, sErr)
		}
	})
}

func TestFI1ContextCancellationDuringSend(t *testing.T) {
	m := testManagerWithStream(t, nil, func(_ string, _ []string, _ io.Reader, _ io.Writer) fakeStreamResult {
		return fakeStreamResult{err: context.DeadlineExceeded}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := m.Send(ctx, "pool/ds@s1", SendOptions{}, &out)
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
}

func TestFI2PoolSpaceExhaustionClassification(t *testing.T) {
	err := &ZFSError{Err: classifyError("no space left on device", 1)}
	if !errors.Is(err, ErrNoSpace) {
		t.Fatalf("expected ErrNoSpace, got %v", err)
	}
}

func TestFI3InjectedPermissionError(t *testing.T) {
	m := NewMockManager()
	m.SetError("CreateSnapshot", ErrPermission)
	_, err := m.CreateSnapshot(context.Background(), "pool/x", "s1")
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("expected ErrPermission, got %v", err)
	}
}

func TestFI4InjectedBusyError(t *testing.T) {
	m := NewMockManager()
	m.SetError("DestroySnapshot", ErrBusy)
	err := m.DestroySnapshot(context.Background(), "pool/x", "s1")
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
}

func TestFI5InjectedNotFound(t *testing.T) {
	m := NewMockManager()
	m.SetError("GetDatasetInfo", ErrNotFound)
	_, err := m.GetDatasetInfo(context.Background(), "pool/missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDS1DeterministicLifecycleTrace(t *testing.T) {
	ctx := context.Background()
	m := NewMockManager()
	if err := m.CreateDataset(ctx, "pool/ds", DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, "pool/ds", "s0"); err != nil {
		t.Fatal(err)
	}
	if err := m.DestroyDataset(ctx, "pool/ds", DestroyOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
}

func TestDS2DeterministicRollbackTrace(t *testing.T) {
	ctx := context.Background()
	m := NewMockManager()
	_ = m.CreateDataset(ctx, "pool/ds", DatasetOptions{})
	_, _ = m.CreateSnapshot(ctx, "pool/ds", "s0")
	_, _ = m.CreateSnapshot(ctx, "pool/ds", "s1")
	if err := m.Rollback(ctx, "pool/ds", "s0", RollbackOptions{DestroyLater: true}); err != nil {
		t.Fatal(err)
	}
	list, _ := m.ListSnapshots(ctx, "pool/ds")
	if len(list) != 1 || list[0].Name != "s0" {
		t.Fatalf("unexpected snapshots: %+v", list)
	}
}

func TestDS3DeterministicCloneTrace(t *testing.T) {
	ctx := context.Background()
	m := NewMockManager()
	_ = m.CreateDataset(ctx, "pool/base", DatasetOptions{})
	_, _ = m.CreateSnapshot(ctx, "pool/base", "init")
	if err := m.CloneFromSnapshot(ctx, "pool/base@init", "pool/clone"); err != nil {
		t.Fatal(err)
	}
}

func TestST1ConcurrentDatasetCreateDestroy(t *testing.T) {
	ctx := context.Background()
	m := NewMockManager()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ds := fmt.Sprintf("pool/st1-%d", i)
			_ = m.CreateDataset(ctx, ds, DatasetOptions{})
			_ = m.DestroyDataset(ctx, ds, DestroyOptions{})
		}(i)
	}
	wg.Wait()
}

func TestST2ConcurrentSnapshotOps(t *testing.T) {
	ctx := context.Background()
	m := NewMockManager()
	_ = m.CreateDataset(ctx, "pool/st2", DatasetOptions{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := fmt.Sprintf("s-%d", i)
			_, _ = m.CreateSnapshot(ctx, "pool/st2", s)
			_, _ = m.SnapshotExists(ctx, "pool/st2", s)
		}(i)
	}
	wg.Wait()
}

func TestST3ConcurrentErrorInjectionReads(t *testing.T) {
	m := NewMockManager()
	m.SetError("DatasetExists", ErrPermission)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = m.DatasetExists(context.Background(), "pool/x")
		}()
	}
	wg.Wait()
}

func TestSec1RejectTraversalNames(t *testing.T) {
	if err := ValidateName("pool/../evil"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestSec2RejectShellCharsInSnapshotName(t *testing.T) {
	if err := ValidateSnapshotName("bad;name"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestSec3RejectInvalidMountpoint(t *testing.T) {
	if err := ValidateMountpoint("../../etc"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestSec4RejectRestrictedProperty(t *testing.T) {
	if err := ValidatePropertyName("setuid"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func BenchmarkB1ValidateName(b *testing.B) {
	for b.Loop() {
		_ = ValidateName("pool/sessions/bench")
	}
}

func BenchmarkB2ValidateSnapshotName(b *testing.B) {
	for b.Loop() {
		_ = ValidateSnapshotName("turn-001")
	}
}

func BenchmarkB3ClassifyError(b *testing.B) {
	for b.Loop() {
		_ = classifyError("no space left on device", 1)
	}
}

func BenchmarkB4MockCreateDataset(b *testing.B) {
	for b.Loop() {
		m := NewMockManager()
		_ = m.CreateDataset(context.Background(), "pool/bench", DatasetOptions{})
	}
}

func BenchmarkB5MockSnapshotLifecycle(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		m := NewMockManager()
		_ = m.CreateDataset(ctx, "pool/bench", DatasetOptions{})
		_, _ = m.CreateSnapshot(ctx, "pool/bench", "s0")
		_ = m.DestroySnapshot(ctx, "pool/bench", "s0")
	}
}

func BenchmarkB6ParseTabular(b *testing.B) {
	data := []byte("a\tb\tc\n1\t2\t3\n")
	for b.Loop() {
		_, _ = parseTabular(data, 3)
	}
}
