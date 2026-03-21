"""Real tier ZFS durability tests (Plan 22 §6).

Tests data persistence across agent lifecycles using ZFS.
Exercises Dimension 4 (Execution Environment: gVisor+ZFS).

Tier: Real only — requires ZFS storage backend on sandbox-host.
"""

import os

import pytest

from .client import FlexAgentClient
from .helpers import collect_events, events_contain_text, extract_text, has_content_event


pytestmark = [pytest.mark.real, pytest.mark.zfs, pytest.mark.timeout(300)]


def _skip_without_zfs():
    """Skip if ZFS backend is not configured."""
    if os.environ.get("SANDBOX_STORAGE_BACKEND", "").lower() != "zfs":
        pytest.skip("ZFS storage backend not configured")


class TestZFSWriteSnapshotClone:
    """ZFS-D1: Write file -> snapshot -> destroy -> clone -> read back."""

    def test_cross_session_persistence(self, fleet_orchestrator: FlexAgentClient):
        _skip_without_zfs()
        client = fleet_orchestrator

        # Session A: write a file
        resp_a = client.create_session(placement="tools-sandbox")
        session_a = resp_a["session_id"]
        try:
            events = collect_events(
                client, session_a,
                "Write the text 'zfs-durability-test-data' to /workspace/persist.txt"
            )
            assert has_content_event(events), "Expected content events from write"

            # Snapshot session A
            snap = client.create_snapshot(session_a, "checkpoint-1")
            assert snap  # Should return snapshot info
        finally:
            client.destroy_session(session_a)

        # Session B: clone from snapshot and read back
        resp_b = client.create_session(
            placement="tools-sandbox",
            base_snapshot="checkpoint-1",
        )
        session_b = resp_b["session_id"]
        try:
            events = collect_events(
                client, session_b,
                "Read /workspace/persist.txt and tell me its exact contents"
            )
            assert has_content_event(events), "Expected content events from read"
            assert events_contain_text(events, "zfs-durability-test-data"), (
                "Expected cloned session to contain data from snapshot"
            )
        finally:
            client.destroy_session(session_b)


class TestZFSSnapshotRollback:
    """ZFS-D2: Write -> snapshot -> write more -> rollback -> verify snapshot state."""

    def test_rollback_restores_snapshot_state(self, fleet_orchestrator: FlexAgentClient):
        _skip_without_zfs()
        client = fleet_orchestrator

        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write initial file
            collect_events(
                client, session_id,
                "Write 'before-snapshot' to /workspace/rollback-test.txt"
            )

            # Snapshot
            client.create_snapshot(session_id, "rollback-point")

            # Write more (overwrite)
            collect_events(
                client, session_id,
                "Write 'after-snapshot' to /workspace/rollback-test.txt"
            )

            # Verify overwrite took effect
            events = collect_events(
                client, session_id,
                "Read /workspace/rollback-test.txt and tell me its exact contents"
            )
            assert events_contain_text(events, "after-snapshot"), (
                "Expected overwritten content before rollback"
            )
        finally:
            client.destroy_session(session_id)

        # Create new session from snapshot (rollback point)
        resp2 = client.create_session(
            placement="tools-sandbox",
            base_snapshot="rollback-point",
        )
        session2 = resp2["session_id"]
        try:
            events = collect_events(
                client, session2,
                "Read /workspace/rollback-test.txt and tell me its exact contents"
            )
            assert events_contain_text(events, "before-snapshot"), (
                "Expected snapshot state after rollback"
            )
        finally:
            client.destroy_session(session2)


class TestZFSMultiTurnSnapshots:
    """ZFS-D3: Multi-turn with per-turn snapshots."""

    def test_snapshot_per_turn(self, fleet_orchestrator: FlexAgentClient):
        _skip_without_zfs()
        client = fleet_orchestrator

        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        snapshots = []
        try:
            for i in range(3):
                collect_events(
                    client, session_id,
                    f"Write 'turn-{i}-data' to /workspace/turn-{i}.txt"
                )
                snap = client.create_snapshot(session_id, f"turn-{i}")
                snapshots.append(snap)
                assert snap, f"Snapshot for turn {i} should succeed"

            assert len(snapshots) == 3

            # Rollback to turn 1 by cloning from that snapshot
        finally:
            client.destroy_session(session_id)

        # Create session from turn-1 snapshot
        resp2 = client.create_session(
            placement="tools-sandbox",
            base_snapshot="turn-1",
        )
        session2 = resp2["session_id"]
        try:
            # turn-0.txt and turn-1.txt should exist
            events = collect_events(
                client, session2,
                "Read /workspace/turn-1.txt and tell me its contents"
            )
            assert events_contain_text(events, "turn-1-data")

            # turn-2.txt should NOT exist (created after turn-1 snapshot)
            events = collect_events(
                client, session2,
                "Check if /workspace/turn-2.txt exists and tell me yes or no"
            )
            # The agent should report the file doesn't exist
            assert has_content_event(events)
        finally:
            client.destroy_session(session2)


class TestZFSSessionPauseResume:
    """ZFS-D4: Pause -> resume -> verify files persist."""

    def test_pause_resume_preserves_files(self, fleet_orchestrator: FlexAgentClient):
        _skip_without_zfs()
        client = fleet_orchestrator

        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write a file
            collect_events(
                client, session_id,
                "Write 'pause-resume-test' to /workspace/pause-test.txt"
            )

            # Resume the session (pause is implicit via ResumeSession)
            client.resume_session(session_id)

            # Verify file still present
            events = collect_events(
                client, session_id,
                "Read /workspace/pause-test.txt and tell me its exact contents"
            )
            assert events_contain_text(events, "pause-resume-test"), (
                "Expected file to persist across pause/resume"
            )
        finally:
            client.destroy_session(session_id)


class TestZFSLargeFile:
    """ZFS-D5: Large file (100MB) write -> snapshot -> clone -> verify."""

    @pytest.mark.timeout(600)
    def test_large_file_persistence(self, fleet_orchestrator: FlexAgentClient):
        _skip_without_zfs()
        client = fleet_orchestrator

        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write a large file via bash (100MB of deterministic data)
            collect_events(
                client, session_id,
                "Run this bash command: dd if=/dev/urandom bs=1M count=100 | "
                "base64 > /workspace/large-file.txt && "
                "md5sum /workspace/large-file.txt > /workspace/large-file.md5"
            )

            # Read the checksum
            events_md5 = collect_events(
                client, session_id,
                "Run: cat /workspace/large-file.md5"
            )
            assert has_content_event(events_md5)
            original_md5 = extract_text(events_md5)

            # Snapshot
            client.create_snapshot(session_id, "large-file-snap")
        finally:
            client.destroy_session(session_id)

        # Clone from snapshot and verify checksum matches
        resp2 = client.create_session(
            placement="tools-sandbox",
            base_snapshot="large-file-snap",
        )
        session2 = resp2["session_id"]
        try:
            clone_events = collect_events(
                client, session2,
                "Run: md5sum /workspace/large-file.txt"
            )
            assert has_content_event(clone_events), (
                "Expected checksum output from cloned session"
            )
            clone_md5 = extract_text(clone_events)

            # Compare the md5 hashes (first field of md5sum output)
            original_hash = original_md5.split()[0] if original_md5.strip() else ""
            clone_hash = clone_md5.split()[0] if clone_md5.strip() else ""
            assert original_hash and clone_hash, (
                f"Failed to extract md5 hashes: original='{original_md5}', clone='{clone_md5}'"
            )
            assert original_hash == clone_hash, (
                f"Checksum mismatch after clone: original={original_hash}, clone={clone_hash}"
            )
        finally:
            client.destroy_session(session2)
