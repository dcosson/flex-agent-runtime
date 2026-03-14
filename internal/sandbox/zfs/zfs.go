package zfs

import (
	"context"
	"io"
	"time"
)

// ZFSManager provides operations on ZFS datasets, snapshots, and pools.
// All methods are safe for concurrent use.
type ZFSManager interface {
	// Dataset operations.
	CreateDataset(ctx context.Context, name string, opts DatasetOptions) error
	CloneFromSnapshot(ctx context.Context, snapshot, newDataset string) error
	DestroyDataset(ctx context.Context, name string, opts DestroyOptions) error
	GetMountpoint(ctx context.Context, dataset string) (string, error)
	SetMountpoint(ctx context.Context, dataset, mountpoint string) error
	SetProperty(ctx context.Context, dataset, property, value string) error
	GetDatasetInfo(ctx context.Context, name string) (*DatasetInfo, error)
	ListDatasets(ctx context.Context, parent string) ([]DatasetInfo, error)
	DatasetExists(ctx context.Context, name string) (bool, error)

	// Snapshot operations.
	CreateSnapshot(ctx context.Context, dataset, snapName string) (*SnapshotInfo, error)
	Rollback(ctx context.Context, dataset, snapName string, opts RollbackOptions) error
	ListSnapshots(ctx context.Context, dataset string) ([]SnapshotInfo, error)
	DestroySnapshot(ctx context.Context, dataset, snapName string) error
	HoldSnapshot(ctx context.Context, dataset, snapName, tag string) error
	ReleaseSnapshot(ctx context.Context, dataset, snapName, tag string) error
	SnapshotExists(ctx context.Context, dataset, snapName string) (bool, error)

	// Pool operations.
	PoolStatus(ctx context.Context, pool string) (*PoolStatus, error)
	PoolSpace(ctx context.Context, pool string) (*PoolSpace, error)
	ImportPool(ctx context.Context, pool, device string) error
	ExportPool(ctx context.Context, pool string) error

	// Transfer operations.
	EstimateSendSize(ctx context.Context, snapshot string, opts SendOptions) (int64, error)
	Send(ctx context.Context, snapshot string, opts SendOptions, w io.Writer) error
	Receive(ctx context.Context, dataset string, r io.Reader) error
}

type DatasetInfo struct {
	Name       string
	Mountpoint string
	Used       int64
	Available  int64
	Referenced int64
	Origin     string
	Creation   time.Time
}

type SnapshotInfo struct {
	Name     string
	Dataset  string
	Used     int64
	Refer    int64
	Creation time.Time
	Holds    int
}

type PoolStatus struct {
	Name   string
	State  PoolState
	Scan   string
	Errors string
}

type PoolState string

const (
	PoolOnline   PoolState = "ONLINE"
	PoolDegraded PoolState = "DEGRADED"
	PoolFaulted  PoolState = "FAULTED"
	PoolOffline  PoolState = "OFFLINE"
	PoolRemoved  PoolState = "REMOVED"
	PoolUnavail  PoolState = "UNAVAIL"
)

type PoolSpace struct {
	Pool          string
	Size          int64
	Allocated     int64
	Free          int64
	Capacity      float64
	Fragmentation float64
}

type DatasetOptions struct {
	Mountpoint string
	Properties map[string]string
	Quota      int64
}

type DestroyOptions struct {
	Recursive       bool
	Force           bool
	DependentClones bool
}

type RollbackOptions struct {
	DestroyLater bool
}

type SendOptions struct {
	Incremental string
	Raw         bool
	Compressed  bool
	LargeBlocks bool
}
