package zfs

import "context"

func (m *CLIManager) PoolStatus(ctx context.Context, pool string) (*PoolStatus, error) {
	if err := ValidatePoolName(pool); err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, "PoolStatus", "zpool", "status", "-p", pool)
	if err != nil {
		return nil, err
	}
	return parsePoolStatus(pool, out)
}

func (m *CLIManager) PoolSpace(ctx context.Context, pool string) (*PoolSpace, error) {
	if err := ValidatePoolName(pool); err != nil {
		return nil, err
	}
	out, err := m.exec(ctx, "PoolSpace", "zpool", "list", "-H", "-p", "-o", "name,size,allocated,free,capacity,fragmentation", pool)
	if err != nil {
		return nil, err
	}
	return parsePoolSpace(out)
}

func (m *CLIManager) ImportPool(ctx context.Context, pool, device string) error {
	if err := ValidatePoolName(pool); err != nil {
		return err
	}
	if err := ValidateDevicePath(device); err != nil {
		return err
	}
	_, err := m.exec(ctx, "ImportPool", "zpool", "import", "-d", device, pool)
	return err
}

func (m *CLIManager) ExportPool(ctx context.Context, pool string) error {
	if err := ValidatePoolName(pool); err != nil {
		return err
	}
	_, err := m.exec(ctx, "ExportPool", "zpool", "export", pool)
	return err
}
