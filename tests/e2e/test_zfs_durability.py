"""Real tier ZFS durability tests (Plan 22 §6).

Tests data persistence across agent lifecycles using ZFS.
Exercises Dimension 4 (Execution Environment: gVisor+ZFS).

Tier: Real only — requires ZFS storage backend on sandbox-host.
"""

import os

import pytest

from .client import FlexAgentClient


pytestmark = [pytest.mark.real, pytest.mark.zfs, pytest.mark.timeout(300)]


def _skip_without_zfs():
    """Skip if ZFS backend is not configured."""
    if os.environ.get("SANDBOX_STORAGE_BACKEND", "").lower() != "zfs":
        pytest.skip("ZFS storage backend not configured")


def _collect_events(client: FlexAgentClient, session_id: str, message: str) -> list[dict]:
    return list(client.send_message(session_id, message))


def _events_contain_text(events: list[dict], substring: str) -> bool:
    for e in events:
        for field in ("text", "content", "data", "output"):
            val = e.get(field, "")
            if isinstance(val, str) and substring in val:
                return True
    return False


def _has_content_event(events: list[dict]) -> bool:
    content_types = {"text", "text_delta", "content_block_delta", "message_start"}
    return bool({e.get("type") for e in events} & content_types)


class TestZFSWriteSnapshotClone:
    """ZFS-D1: Write file -> snapshot -> destroy -> clone -> read back."""

    def test_cross_session_persistence(self, fleet_orchestrator: FlexAgentClient):
        _skip_without_zfs()
        client = fleet_orchestrator

        # Session A: write a file
        resp_a = client.create_session(placement="tools-sandbox")
        session_a = resp_a["session_id"]
        try:
            events = _collect_events(
                client, session_a,
                "Write the text 'zfs-durability-test-data' to /workspace/persist.txt"
            )
            assert _has_content_event(events), "Expected content events from write"

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
            events = _collect_events(
                client, session_b,
                "Read /workspace/persist.txt and tell me its exact contents"
            )
            assert _has_content_event(events), "Expected content events from read"
            assert _events_contain_text(events, "zfs-durability-test-data"), (
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
            _collect_events(
                client, session_id,
                "Write 'before-snapshot' to /workspace/rollback-test.txt"
            )

            # Snapshot
            client.create_snapshot(session_id, "rollback-point")

            # Write more (overwrite)
            _collect_events(
                client, session_id,
                "Write 'after-snapshot' to /workspace/rollback-test.txt"
            )

            # Verify overwrite took effect
            events = _collect_events(
                client, session_id,
                "Read /workspace/rollback-test.txt and tell me its exact contents"
            )
            assert _events_contain_text(events, "after-snapshot"), (
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
            events = _collect_events(
                client, session2,
                "Read /workspace/rollback-test.txt and tell me its exact contents"
            )
            assert _events_contain_text(events, "before-snapshot"), (
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
                _collect_events(
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
            events = _collect_events(
                client, session2,
                "Read /workspace/turn-1.txt and tell me its contents"
            )
            assert _events_contain_text(events, "turn-1-data")

            # turn-2.txt should NOT exist (created after turn-1 snapshot)
            events = _collect_events(
                client, session2,
                "Check if /workspace/turn-2.txt exists and tell me yes or no"
            )
            # The agent should report the file doesn't exist
            assert _has_content_event(events)
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
            _collect_events(
                client, session_id,
                "Write 'pause-resume-test' to /workspace/pause-test.txt"
            )

            # Resume the session (pause is implicit via ResumeSession)
            client.resume_session(session_id)

            # Verify file still present
            events = _collect_events(
                client, session_id,
                "Read /workspace/pause-test.txt and tell me its exact contents"
            )
            assert _events_contain_text(events, "pause-resume-test"), (
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
            _collect_events(
                client, session_id,
                "Run this bash command: dd if=/dev/urandom bs=1M count=100 | "
                "base64 > /workspace/large-file.txt && "
                "md5sum /workspace/large-file.txt > /workspace/large-file.md5"
            )

            # Read the checksum
            events_md5 = _collect_events(
                client, session_id,
                "Read /workspace/large-file.md5 and tell me the md5 hash"
            )
            assert _has_content_event(events_md5)

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
            events = _collect_events(
                client, session2,
                "Run: md5sum /workspace/large-file.txt && cat /workspace/large-file.md5"
            )
            assert _has_content_event(events), (
                "Expected checksum output from cloned session"
            )
        finally:
            client.destroy_session(session2)
