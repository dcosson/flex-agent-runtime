package zfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type mockDataset struct {
	info DatasetInfo
}

type mockSnapshot struct {
	info SnapshotInfo
	tags map[string]struct{}
	seq  int64
}

// MockManager implements ZFSManager in-memory for tests.
type MockManager struct {
	mu sync.RWMutex

	datasets  map[string]*mockDataset
	snapshots map[string]*mockSnapshot // dataset@snapshot
	errors    map[string]error         // op -> error injection
	seq       int64
}

func NewMockManager() *MockManager {
	return &MockManager{
		datasets:  make(map[string]*mockDataset),
		snapshots: make(map[string]*mockSnapshot),
		errors:    make(map[string]error),
	}
}

func (m *MockManager) SetError(op string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		delete(m.errors, op)
		return
	}
	m.errors[op] = err
}

func (m *MockManager) ClearError(op string) {
	m.SetError(op, nil)
}

func (m *MockManager) injected(op string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.errors[op]
}

func (m *MockManager) CreateDataset(_ context.Context, name string, opts DatasetOptions) error {
	if err := m.injected("CreateDataset"); err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	if opts.Mountpoint != "" {
		if err := ValidateMountpoint(opts.Mountpoint); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.datasets[name]; ok {
		return errors.Join(ErrAlreadyExists, ErrDatasetExists)
	}
	mountpoint := opts.Mountpoint
	if mountpoint == "" {
		mountpoint = "/" + name
	}
	m.datasets[name] = &mockDataset{info: DatasetInfo{
		Name:       name,
		Mountpoint: mountpoint,
		Available:  opts.Quota,
		Creation:   time.Now().UTC(),
	}}
	return nil
}

func (m *MockManager) CloneFromSnapshot(_ context.Context, snapshot, newDataset string) error {
	if err := m.injected("CloneFromSnapshot"); err != nil {
		return err
	}
	if err := ValidateSnapshotFullName(snapshot); err != nil {
		return err
	}
	if err := ValidateName(newDataset); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.datasets[newDataset]; ok {
		return errors.Join(ErrAlreadyExists, ErrDatasetExists)
	}
	ms, ok := m.snapshots[snapshot]
	if !ok {
		return errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	m.datasets[newDataset] = &mockDataset{info: DatasetInfo{
		Name:       newDataset,
		Mountpoint: "/" + newDataset,
		Origin:     snapshot,
		Used:       ms.info.Refer,
		Referenced: ms.info.Refer,
		Creation:   time.Now().UTC(),
	}}
	return nil
}

func (m *MockManager) DestroyDataset(_ context.Context, name string, opts DestroyOptions) error {
	if err := m.injected("DestroyDataset"); err != nil {
		return err
	}
	if err := ValidateName(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.datasets[name]; !ok {
		return errors.Join(ErrNotFound, ErrDatasetNotFound)
	}
	if !opts.Recursive {
		prefix := name + "/"
		for dataset := range m.datasets {
			if strings.HasPrefix(dataset, prefix) {
				return ErrBusy
			}
		}
	}
	delete(m.datasets, name)
	for key := range m.snapshots {
		if strings.HasPrefix(key, name+"@") {
			delete(m.snapshots, key)
		}
	}
	return nil
}

func (m *MockManager) GetMountpoint(_ context.Context, dataset string) (string, error) {
	if err := m.injected("GetMountpoint"); err != nil {
		return "", err
	}
	if err := ValidateName(dataset); err != nil {
		return "", err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ds, ok := m.datasets[dataset]
	if !ok {
		return "", errors.Join(ErrNotFound, ErrDatasetNotFound)
	}
	return ds.info.Mountpoint, nil
}

func (m *MockManager) SetMountpoint(_ context.Context, dataset, mountpoint string) error {
	if err := m.injected("SetMountpoint"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateMountpoint(mountpoint); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ds, ok := m.datasets[dataset]
	if !ok {
		return errors.Join(ErrNotFound, ErrDatasetNotFound)
	}
	ds.info.Mountpoint = mountpoint
	return nil
}

func (m *MockManager) SetProperty(_ context.Context, dataset, property, value string) error {
	if err := m.injected("SetProperty"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidatePropertyName(property); err != nil {
		return err
	}
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("%w: property value contains newline", ErrInvalidName)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ds, ok := m.datasets[dataset]
	if !ok {
		return errors.Join(ErrNotFound, ErrDatasetNotFound)
	}
	if ds.info.Available == 0 && property == "quota" {
		quota, err := parseSize(value)
		if err == nil {
			ds.info.Available = quota
		}
	}
	return nil
}

func (m *MockManager) GetDatasetInfo(_ context.Context, name string) (*DatasetInfo, error) {
	if err := m.injected("GetDatasetInfo"); err != nil {
		return nil, err
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ds, ok := m.datasets[name]
	if !ok {
		return nil, errors.Join(ErrNotFound, ErrDatasetNotFound)
	}
	copy := ds.info
	return &copy, nil
}

func (m *MockManager) ListDatasets(_ context.Context, parent string) ([]DatasetInfo, error) {
	if err := m.injected("ListDatasets"); err != nil {
		return nil, err
	}
	if err := ValidateName(parent); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]DatasetInfo, 0)
	for name, ds := range m.datasets {
		if name == parent || strings.HasPrefix(name, parent+"/") {
			items = append(items, ds.info)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

func (m *MockManager) DatasetExists(ctx context.Context, name string) (bool, error) {
	if err := m.injected("DatasetExists"); err != nil {
		return false, err
	}
	_, err := m.GetDatasetInfo(ctx, name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrDatasetNotFound) || errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (m *MockManager) CreateSnapshot(_ context.Context, dataset, snapName string) (*SnapshotInfo, error) {
	if err := m.injected("CreateSnapshot"); err != nil {
		return nil, err
	}
	if err := ValidateName(dataset); err != nil {
		return nil, err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ds, ok := m.datasets[dataset]
	if !ok {
		return nil, errors.Join(ErrNotFound, ErrDatasetNotFound)
	}
	key := dataset + "@" + snapName
	if _, ok := m.snapshots[key]; ok {
		return nil, errors.Join(ErrAlreadyExists, ErrSnapshotExists)
	}
	ms := &mockSnapshot{info: SnapshotInfo{
		Name:     snapName,
		Dataset:  dataset,
		Refer:    ds.info.Referenced,
		Used:     0,
		Creation: time.Now().UTC(),
	}, tags: make(map[string]struct{}), seq: m.seq}
	m.seq++
	m.snapshots[key] = ms
	copy := ms.info
	return &copy, nil
}

func (m *MockManager) Rollback(_ context.Context, dataset, snapName string, opts RollbackOptions) error {
	if err := m.injected("Rollback"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	targetKey := dataset + "@" + snapName
	target, ok := m.snapshots[targetKey]
	if !ok {
		return errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	if !opts.DestroyLater {
		return nil
	}
	targetSeq := target.seq
	for key, snap := range m.snapshots {
		if !strings.HasPrefix(key, dataset+"@") {
			continue
		}
		if snap.seq > targetSeq {
			delete(m.snapshots, key)
		}
	}
	return nil
}

func (m *MockManager) ListSnapshots(_ context.Context, dataset string) ([]SnapshotInfo, error) {
	if err := m.injected("ListSnapshots"); err != nil {
		return nil, err
	}
	if err := ValidateName(dataset); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]SnapshotInfo, 0)
	for key, snap := range m.snapshots {
		if strings.HasPrefix(key, dataset+"@") {
			si := snap.info
			si.Holds = len(snap.tags)
			items = append(items, si)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Creation.Before(items[j].Creation) })
	return items, nil
}

func (m *MockManager) DestroySnapshot(_ context.Context, dataset, snapName string) error {
	if err := m.injected("DestroySnapshot"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := dataset + "@" + snapName
	snap, ok := m.snapshots[key]
	if !ok {
		return errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	if len(snap.tags) > 0 {
		return ErrSnapshotHeld
	}
	delete(m.snapshots, key)
	return nil
}

func (m *MockManager) HoldSnapshot(_ context.Context, dataset, snapName, tag string) error {
	if err := m.injected("HoldSnapshot"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	if err := ValidateHoldTag(tag); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := dataset + "@" + snapName
	snap, ok := m.snapshots[key]
	if !ok {
		return errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	snap.tags[tag] = struct{}{}
	return nil
}

func (m *MockManager) ReleaseSnapshot(_ context.Context, dataset, snapName, tag string) error {
	if err := m.injected("ReleaseSnapshot"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	if err := ValidateHoldTag(tag); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := dataset + "@" + snapName
	snap, ok := m.snapshots[key]
	if !ok {
		return errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	delete(snap.tags, tag)
	return nil
}

func (m *MockManager) SnapshotExists(_ context.Context, dataset, snapName string) (bool, error) {
	if err := m.injected("SnapshotExists"); err != nil {
		return false, err
	}
	if err := ValidateName(dataset); err != nil {
		return false, err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return false, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.snapshots[dataset+"@"+snapName]
	return ok, nil
}

func (m *MockManager) PoolStatus(_ context.Context, pool string) (*PoolStatus, error) {
	if err := m.injected("PoolStatus"); err != nil {
		return nil, err
	}
	if err := ValidatePoolName(pool); err != nil {
		return nil, err
	}
	if pool == "missing" {
		return nil, errors.Join(ErrNotFound, ErrPoolNotFound)
	}
	return &PoolStatus{Name: pool, State: PoolOnline}, nil
}

func (m *MockManager) PoolSpace(_ context.Context, pool string) (*PoolSpace, error) {
	if err := m.injected("PoolSpace"); err != nil {
		return nil, err
	}
	if err := ValidatePoolName(pool); err != nil {
		return nil, err
	}
	if pool == "missing" {
		return nil, errors.Join(ErrNotFound, ErrPoolNotFound)
	}
	return &PoolSpace{Pool: pool}, nil
}

func (m *MockManager) ImportPool(_ context.Context, pool, device string) error {
	if err := m.injected("ImportPool"); err != nil {
		return err
	}
	if err := ValidatePoolName(pool); err != nil {
		return err
	}
	return ValidateDevicePath(device)
}

func (m *MockManager) ExportPool(_ context.Context, pool string) error {
	if err := m.injected("ExportPool"); err != nil {
		return err
	}
	return ValidatePoolName(pool)
}

func (m *MockManager) EstimateSendSize(_ context.Context, snapshot string, opts SendOptions) (int64, error) {
	if err := m.injected("EstimateSendSize"); err != nil {
		return 0, err
	}
	if err := ValidateSnapshotFullName(snapshot); err != nil {
		return 0, err
	}
	if opts.Incremental != "" {
		if err := ValidateSnapshotFullName(opts.Incremental); err != nil {
			return 0, err
		}
	}
	m.mu.RLock()
	_, ok := m.snapshots[snapshot]
	m.mu.RUnlock()
	if !ok {
		return 0, errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	return 0, nil
}

func (m *MockManager) Send(_ context.Context, snapshot string, opts SendOptions, w io.Writer) error {
	if err := m.injected("Send"); err != nil {
		return err
	}
	if err := ValidateSnapshotFullName(snapshot); err != nil {
		return err
	}
	if opts.Incremental != "" {
		if err := ValidateSnapshotFullName(opts.Incremental); err != nil {
			return err
		}
	}
	m.mu.RLock()
	_, ok := m.snapshots[snapshot]
	m.mu.RUnlock()
	if !ok {
		return errors.Join(ErrNotFound, ErrSnapshotNotFound)
	}
	if w == nil {
		return fmt.Errorf("writer is nil")
	}
	_, err := io.WriteString(w, "mock-zfs-stream")
	return err
}

func (m *MockManager) Receive(_ context.Context, dataset string, r io.Reader) error {
	if err := m.injected("Receive"); err != nil {
		return err
	}
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("reader is nil")
	}
	_, err := io.Copy(io.Discard, r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.datasets[dataset]; !ok {
		m.datasets[dataset] = &mockDataset{info: DatasetInfo{
			Name:       dataset,
			Mountpoint: "/" + dataset,
			Creation:   time.Now().UTC(),
		}}
	}
	return nil
}
