package zfs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (m *CLIManager) CreateSnapshot(ctx context.Context, dataset, snapName string) (*SnapshotInfo, error) {
	if err := ValidateName(dataset); err != nil {
		return nil, err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return nil, err
	}
	full := dataset + "@" + snapName
	if _, err := m.exec(ctx, "CreateSnapshot", "snapshot", full); err != nil {
		return nil, err
	}
	return m.getSnapshotInfo(ctx, dataset, snapName)
}

func (m *CLIManager) Rollback(ctx context.Context, dataset, snapName string, opts RollbackOptions) error {
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	args := []string{"rollback"}
	if opts.DestroyLater {
		args = append(args, "-r")
	}
	args = append(args, dataset+"@"+snapName)
	_, err := m.exec(ctx, "Rollback", args...)
	return err
}

func (m *CLIManager) ListSnapshots(ctx context.Context, dataset string) ([]SnapshotInfo, error) {
	if err := ValidateName(dataset); err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, "ListSnapshots", "list", "-H", "-p", "-t", "snapshot", "-o", "name,used,referenced,creation,userrefs", "-s", "creation", dataset)
	if err != nil {
		return nil, err
	}
	return parseSnapshotList(dataset, out)
}

func (m *CLIManager) DestroySnapshot(ctx context.Context, dataset, snapName string) error {
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	_, err := m.exec(ctx, "DestroySnapshot", "destroy", dataset+"@"+snapName)
	return err
}

func (m *CLIManager) HoldSnapshot(ctx context.Context, dataset, snapName, tag string) error {
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	if err := ValidateHoldTag(tag); err != nil {
		return err
	}
	_, err := m.exec(ctx, "HoldSnapshot", "hold", tag, dataset+"@"+snapName)
	return err
}

func (m *CLIManager) ReleaseSnapshot(ctx context.Context, dataset, snapName, tag string) error {
	if err := ValidateName(dataset); err != nil {
		return err
	}
	if err := ValidateSnapshotName(snapName); err != nil {
		return err
	}
	if err := ValidateHoldTag(tag); err != nil {
		return err
	}
	_, err := m.exec(ctx, "ReleaseSnapshot", "release", tag, dataset+"@"+snapName)
	return err
}

func (m *CLIManager) SnapshotExists(ctx context.Context, dataset, snapName string) (bool, error) {
	_, err := m.getSnapshotInfo(ctx, dataset, snapName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *CLIManager) getSnapshotInfo(ctx context.Context, dataset, snapName string) (*SnapshotInfo, error) {
	full := dataset + "@" + snapName
	out, err := m.exec(ctx, "GetSnapshotInfo", "list", "-H", "-p", "-t", "snapshot", "-o", "name,used,referenced,creation,userrefs", full)
	if err != nil {
		return nil, err
	}
	rows, err := parseTabular(out, 5)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, fmt.Errorf("expected 1 snapshot row, got %d", len(rows))
	}
	infos, err := parseSnapshotRows(rows)
	if err != nil {
		return nil, err
	}
	if len(infos) != 1 {
		return nil, fmt.Errorf("expected 1 snapshot info, got %d", len(infos))
	}
	return &infos[0], nil
}

func parseSnapshotList(dataset string, out []byte) ([]SnapshotInfo, error) {
	rows, err := parseTabular(out, 5)
	if err != nil {
		return nil, err
	}
	infos, err := parseSnapshotRows(rows)
	if err != nil {
		return nil, err
	}
	for i := range infos {
		if infos[i].Dataset == "" {
			infos[i].Dataset = dataset
		}
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Creation.Before(infos[j].Creation)
	})
	return infos, nil
}

func parseSnapshotRows(rows [][]string) ([]SnapshotInfo, error) {
	items := make([]SnapshotInfo, 0, len(rows))
	for _, row := range rows {
		used, err := parseSize(row[1])
		if err != nil {
			return nil, err
		}
		refer, err := parseSize(row[2])
		if err != nil {
			return nil, err
		}
		created, err := parseTimestamp(row[3])
		if err != nil {
			return nil, err
		}
		holds64, err := parseSize(row[4])
		if err != nil {
			return nil, err
		}
		full := strings.TrimSpace(row[0])
		parts := strings.SplitN(full, "@", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid snapshot full name %q", full)
		}
		items = append(items, SnapshotInfo{
			Name:     parts[1],
			Dataset:  parts[0],
			Used:     used,
			Refer:    refer,
			Creation: created,
			Holds:    int(holds64),
		})
	}
	return items, nil
}
