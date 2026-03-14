package zfs

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestMockManagerDatasetLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMockManager()

	if err := m.CreateDataset(ctx, "pool/sessions/s1", DatasetOptions{Mountpoint: "/mnt/s1"}); err != nil {
		t.Fatalf("CreateDataset error = %v", err)
	}

	exists, err := m.DatasetExists(ctx, "pool/sessions/s1")
	if err != nil || !exists {
		t.Fatalf("DatasetExists = (%v, %v), want (true, nil)", exists, err)
	}

	mp, err := m.GetMountpoint(ctx, "pool/sessions/s1")
	if err != nil || mp != "/mnt/s1" {
		t.Fatalf("GetMountpoint = (%q, %v)", mp, err)
	}

	if err := m.SetMountpoint(ctx, "pool/sessions/s1", "/mnt/new"); err != nil {
		t.Fatalf("SetMountpoint error = %v", err)
	}
	info, err := m.GetDatasetInfo(ctx, "pool/sessions/s1")
	if err != nil {
		t.Fatalf("GetDatasetInfo error = %v", err)
	}
	if info.Mountpoint != "/mnt/new" {
		t.Fatalf("mountpoint = %q, want /mnt/new", info.Mountpoint)
	}

	if err := m.DestroyDataset(ctx, "pool/sessions/s1", DestroyOptions{}); err != nil {
		t.Fatalf("DestroyDataset error = %v", err)
	}

	exists, err = m.DatasetExists(ctx, "pool/sessions/s1")
	if err != nil || exists {
		t.Fatalf("DatasetExists after destroy = (%v, %v), want (false, nil)", exists, err)
	}
}

func TestMockManagerCloneFromSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMockManager()
	if err := m.CreateDataset(ctx, "pool/base", DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, "pool/base", "init"); err != nil {
		t.Fatal(err)
	}
	if err := m.CloneFromSnapshot(ctx, "pool/base@init", "pool/sessions/s1"); err != nil {
		t.Fatalf("CloneFromSnapshot error = %v", err)
	}
	info, err := m.GetDatasetInfo(ctx, "pool/sessions/s1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Origin != "pool/base@init" {
		t.Fatalf("origin = %q, want pool/base@init", info.Origin)
	}
}

func TestMockManagerErrorInjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMockManager()
	m.SetError("CreateDataset", ErrPermission)
	err := m.CreateDataset(ctx, "pool/sessions/s1", DatasetOptions{})
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("expected ErrPermission, got %v", err)
	}
	m.ClearError("CreateDataset")
	if err := m.CreateDataset(ctx, "pool/sessions/s1", DatasetOptions{}); err != nil {
		t.Fatalf("CreateDataset after clear error = %v", err)
	}
}

func TestMockManagerSnapshotHoldRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMockManager()
	if err := m.CreateDataset(ctx, "pool/sessions/s1", DatasetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateSnapshot(ctx, "pool/sessions/s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.HoldSnapshot(ctx, "pool/sessions/s1", "turn-1", "protect"); err != nil {
		t.Fatal(err)
	}
	if err := m.DestroySnapshot(ctx, "pool/sessions/s1", "turn-1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	if err := m.ReleaseSnapshot(ctx, "pool/sessions/s1", "turn-1", "protect"); err != nil {
		t.Fatal(err)
	}
	if err := m.DestroySnapshot(ctx, "pool/sessions/s1", "turn-1"); err != nil {
		t.Fatal(err)
	}
}

func TestMockManagerSendReceive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewMockManager()
	var out bytes.Buffer
	if err := m.Send(ctx, "pool/base@init", SendOptions{}, &out); err != nil {
		t.Fatalf("Send error = %v", err)
	}
	if out.Len() == 0 {
		t.Fatalf("expected non-empty stream")
	}
	if err := m.Receive(ctx, "pool/target", bytes.NewReader(out.Bytes())); err != nil {
		t.Fatalf("Receive error = %v", err)
	}
}
