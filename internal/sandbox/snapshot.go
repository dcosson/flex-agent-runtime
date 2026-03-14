package sandbox

import (
	"context"
	"fmt"

	"h2-agent-runtime/internal/sandbox/zfs"
)

func (svc *SandboxHostService) TurnComplete(ctx context.Context, sessionID string) (*SnapshotResult, error) {
	sess, err := svc.getSession(sessionID)
	if err != nil {
		return nil, err
	}

	sess.mu.Lock()
	if sess.state != SessionActive || sess.rollingBack {
		sess.mu.Unlock()
		return nil, fmt.Errorf("%w: session is %s", ErrInvalidState, sess.state)
	}
	prospectiveTurn := sess.turnCount + 1
	snapName := fmt.Sprintf("%s-%04d", svc.config.SnapshotPrefix, prospectiveTurn)
	dataset := sess.dataset
	sess.mu.Unlock()

	info, err := svc.zfs.CreateSnapshot(ctx, dataset, snapName)
	if err != nil {
		return nil, fmt.Errorf("snapshot turn %d: %w", prospectiveTurn, err)
	}

	sess.mu.Lock()
	sess.turnCount = prospectiveTurn
	sess.snapshots = append(sess.snapshots, SnapshotEntry{Name: snapName, IsTurnSnapshot: true})
	sess.mu.Unlock()

	if svc.config.MaxSnapshotsPerSession > 0 {
		svc.cleanupOldSnapshots(ctx, sess)
	}

	return &SnapshotResult{SnapshotID: snapName, TurnNumber: prospectiveTurn, SpaceUsed: info.Used}, nil
}

func (svc *SandboxHostService) CreateSnapshot(ctx context.Context, sessionID string, name string) (*SnapshotResult, error) {
	sess, err := svc.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.RLock()
	dataset := sess.dataset
	sess.mu.RUnlock()
	info, err := svc.zfs.CreateSnapshot(ctx, dataset, name)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: %w", name, err)
	}
	sess.mu.Lock()
	sess.snapshots = append(sess.snapshots, SnapshotEntry{Name: name, IsTurnSnapshot: false})
	sess.mu.Unlock()
	return &SnapshotResult{SnapshotID: name, SpaceUsed: info.Used}, nil
}

func (svc *SandboxHostService) RollbackSession(ctx context.Context, sessionID string, snapshotID string) error {
	sess, err := svc.getSession(sessionID)
	if err != nil {
		return err
	}

	sess.mu.Lock()
	if sess.state != SessionActive {
		sess.mu.Unlock()
		return fmt.Errorf("%w: session is %s", ErrInvalidState, sess.state)
	}
	if sess.activeTools.Load() > 0 {
		sess.mu.Unlock()
		return ErrToolsInFlight
	}
	sess.rollingBack = true
	dataset := sess.dataset
	sess.mu.Unlock()

	rollbackErr := svc.zfs.Rollback(ctx, dataset, snapshotID, zfs.RollbackOptions{DestroyLater: true})

	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.rollingBack = false
	if rollbackErr != nil {
		return fmt.Errorf("rollback to %s: %w", snapshotID, rollbackErr)
	}
	targetIdx := -1
	for i, s := range sess.snapshots {
		if s.Name == snapshotID {
			targetIdx = i
			break
		}
	}
	if targetIdx >= 0 {
		sess.snapshots = sess.snapshots[:targetIdx+1]
		turnCount := 0
		for _, s := range sess.snapshots {
			if s.IsTurnSnapshot {
				turnCount++
			}
		}
		sess.turnCount = turnCount
	}
	return nil
}

func (svc *SandboxHostService) ListSnapshots(ctx context.Context, sessionID string) ([]zfs.SnapshotInfo, error) {
	sess, err := svc.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	sess.mu.RLock()
	dataset := sess.dataset
	sess.mu.RUnlock()
	return svc.zfs.ListSnapshots(ctx, dataset)
}

func (svc *SandboxHostService) cleanupOldSnapshots(ctx context.Context, sess *Session) {
	sess.mu.Lock()
	if svc.config.MaxSnapshotsPerSession <= 0 || len(sess.snapshots) <= svc.config.MaxSnapshotsPerSession {
		sess.mu.Unlock()
		return
	}
	toDelete := append([]SnapshotEntry{}, sess.snapshots[:len(sess.snapshots)-svc.config.MaxSnapshotsPerSession]...)
	sess.snapshots = append([]SnapshotEntry{}, sess.snapshots[len(sess.snapshots)-svc.config.MaxSnapshotsPerSession:]...)
	sess.mu.Unlock()

	for _, snap := range toDelete {
		_ = svc.zfs.DestroySnapshot(ctx, sess.dataset, snap.Name)
	}
}
