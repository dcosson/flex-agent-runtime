package zfs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (m *CLIManager) CreateDataset(ctx context.Context, name string, opts DatasetOptions) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	args := []string{"create"}
	if opts.Mountpoint != "" {
		if err := ValidateMountpoint(opts.Mountpoint); err != nil {
			return err
		}
		args = append(args, "-o", "mountpoint="+opts.Mountpoint)
	}
	if opts.Quota > 0 {
		args = append(args, "-o", fmt.Sprintf("quota=%d", opts.Quota))
	}
	if len(opts.Properties) > 0 {
		keys := make([]string, 0, len(opts.Properties))
		for k := range opts.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := ValidatePropertyName(k); err != nil {
				return err
			}
			args = append(args, "-o", k+"="+opts.Properties[k])
		}
	}
	args = append(args, name)
	_, err := m.exec(ctx, "CreateDataset", args...)
	return err
}

func (m *CLIManager) CloneFromSnapshot(ctx context.Context, snapshot, newDataset string) error {
	if err := ValidateSnapshotFullName(snapshot); err != nil {
		return err
	}
	if err := ValidateName(newDataset); err != nil {
		return err
	}
	_, err := m.exec(ctx, "CloneFromSnapshot", "clone", snapshot, newDataset)
	return err
}

func (m *CLIManager) DestroyDataset(ctx context.Context, name string, opts DestroyOptions) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	args := []string{"destroy"}
	if opts.Recursive {
		args = append(args, "-r")
	}
	if opts.Force {
		args = append(args, "-f")
	}
	if opts.DependentClones {
		args = append(args, "-R")
	}
	args = append(args, name)
	_, err := m.exec(ctx, "DestroyDataset", args...)
	return err
}

func (m *CLIManager) GetMountpoint(ctx context.Context, dataset string) (string, error) {
	if err := ValidateName(dataset); err != nil {
		return "", err
	}
	out, err := m.exec(ctx, "GetMountpoint", "get", "-H", "-o", "value", "mountpoint", dataset)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *CLIManager) SetMountpoint(ctx context.Context, dataset, mountpoint string) error {
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateMountpoint(mountpoint); err != nil {
		return err
	}
	_, err := m.exec(ctx, "SetMountpoint", "set", "mountpoint="+mountpoint, dataset)
	return err
}

func (m *CLIManager) GetDatasetInfo(ctx context.Context, name string) (*DatasetInfo, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, "GetDatasetInfo", "get", "-H", "-p", "-o", "value", "mountpoint,used,available,referenced,origin,creation", name)
	if err != nil {
		return nil, err
	}
	return parseDatasetInfo(name, out)
}

func (m *CLIManager) ListDatasets(ctx context.Context, parent string) ([]DatasetInfo, error) {
	if err := ValidateName(parent); err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, "ListDatasets", "list", "-H", "-p", "-r", "-t", "filesystem", "-o", "name,mountpoint,used,available,referenced,origin,creation", parent)
	if err != nil {
		return nil, err
	}
	return parseDatasetInfoList(out)
}

func (m *CLIManager) DatasetExists(ctx context.Context, name string) (bool, error) {
	_, err := m.GetDatasetInfo(ctx, name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func parseDatasetInfo(name string, out []byte) (*DatasetInfo, error) {
	values := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(values) != 6 {
		return nil, fmt.Errorf("expected 6 values for dataset info, got %d", len(values))
	}
	used, err := parseSize(values[1])
	if err != nil {
		return nil, err
	}
	avail, err := parseSize(values[2])
	if err != nil {
		return nil, err
	}
	refer, err := parseSize(values[3])
	if err != nil {
		return nil, err
	}
	created, err := parseTimestamp(values[5])
	if err != nil {
		return nil, err
	}
	origin := strings.TrimSpace(values[4])
	if origin == "-" {
		origin = ""
	}
	return &DatasetInfo{
		Name:       name,
		Mountpoint: strings.TrimSpace(values[0]),
		Used:       used,
		Available:  avail,
		Referenced: refer,
		Origin:     origin,
		Creation:   created,
	}, nil
}

func parseDatasetInfoList(out []byte) ([]DatasetInfo, error) {
	rows, err := parseTabular(out, 7)
	if err != nil {
		return nil, err
	}
	items := make([]DatasetInfo, 0, len(rows))
	for _, row := range rows {
		used, err := parseSize(row[2])
		if err != nil {
			return nil, err
		}
		avail, err := parseSize(row[3])
		if err != nil {
			return nil, err
		}
		refer, err := parseSize(row[4])
		if err != nil {
			return nil, err
		}
		created, err := parseTimestamp(row[6])
		if err != nil {
			return nil, err
		}
		origin := strings.TrimSpace(row[5])
		if origin == "-" {
			origin = ""
		}
		items = append(items, DatasetInfo{
			Name:       strings.TrimSpace(row[0]),
			Mountpoint: strings.TrimSpace(row[1]),
			Used:       used,
			Available:  avail,
			Referenced: refer,
			Origin:     origin,
			Creation:   created,
		})
	}
	return items, nil
}
