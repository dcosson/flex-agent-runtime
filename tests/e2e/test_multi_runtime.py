"""Real tier multi-runtime tests (Plan 22 §8).

Tests interop between different agent driver types: Claude Code, Codex,
and cross-runtime context transfer via ZFS snapshots.

Tier: Real only — requires agent binaries and ZFS.
"""

import os

import pytest

from .client import FlexAgentClient
from .helpers import collect_events, events_contain_text, has_content_event


pytestmark = [pytest.mark.real, pytest.mark.multi_runtime, pytest.mark.timeout(300)]


def _skip_without_binary(env_var: str, name: str):
    """Skip if agent binary is not available."""
    if not os.environ.get(env_var):
        pytest.skip(f"{name} binary not configured ({env_var} not set)")


class TestClaudeCodeSandbox:
    """MR-CC1/CC2: Claude Code driver in agent-sandbox mode."""

    def test_claude_code_events(self, fleet_orchestrator: FlexAgentClient):
        """MR-CC1: Launch Claude Code -> verify events stream back."""
        _skip_without_binary("CLAUDE_CODE_BINARY", "Claude Code")
        client = fleet_orchestrator

        resp = client.create_session(
            placement="agent-sandbox",
            agent_type="claude-code",
        )
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Write 'hello from claude code' to /workspace/cc-test.txt"
            )
            assert len(events) > 0, "Expected events from Claude Code"
            assert has_content_event(events), "Expected content events"
        finally:
            client.destroy_session(session_id)

    @pytest.mark.zfs
    def test_claude_code_snapshot(self, fleet_orchestrator: FlexAgentClient):
        """MR-CC2: Claude Code writes files -> snapshot captures changes."""
        _skip_without_binary("CLAUDE_CODE_BINARY", "Claude Code")
        client = fleet_orchestrator

        resp = client.create_session(
            placement="agent-sandbox",
            agent_type="claude-code",
        )
        session_id = resp["session_id"]
        try:
            collect_events(
                client, session_id,
                "Create a file /workspace/cc-snapshot-test.txt with content 'cc-data'"
            )

            snap = client.create_snapshot(session_id, "cc-snap")
            assert snap, "Snapshot should succeed for Claude Code session"
        finally:
            client.destroy_session(session_id)

        # Verify snapshot has the file by cloning
        resp2 = client.create_session(
            placement="tools-sandbox",
            base_snapshot="cc-snap",
        )
        session2 = resp2["session_id"]
        try:
            events = collect_events(
                client, session2,
                "Read /workspace/cc-snapshot-test.txt and tell me its contents"
            )
            assert events_contain_text(events, "cc-data"), (
                "Expected snapshot to contain Claude Code's file"
            )
        finally:
            client.destroy_session(session2)


class TestCodexSandbox:
    """MR-CX1/CX2: Codex driver in agent-sandbox mode."""

    def test_codex_events(self, fleet_orchestrator: FlexAgentClient):
        """MR-CX1: Launch Codex -> verify events stream back."""
        _skip_without_binary("CODEX_BINARY", "Codex")
        client = fleet_orchestrator

        resp = client.create_session(
            placement="agent-sandbox",
            agent_type="codex",
        )
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Write 'hello from codex' to /workspace/codex-test.txt"
            )
            assert len(events) > 0, "Expected events from Codex"
            assert has_content_event(events), "Expected content events"
        finally:
            client.destroy_session(session_id)

    def test_codex_tool_execution(self, fleet_orchestrator: FlexAgentClient):
        """MR-CX2: Codex can execute tools inside sandbox."""
        _skip_without_binary("CODEX_BINARY", "Codex")
        client = fleet_orchestrator

        resp = client.create_session(
            placement="agent-sandbox",
            agent_type="codex",
        )
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Run the command 'echo codex-tool-test' and tell me the output"
            )
            assert has_content_event(events), "Expected content events from tool execution"
        finally:
            client.destroy_session(session_id)


class TestCrossRuntimeTransfer:
    """MR-CT1/CT2: Cross-runtime context transfer via ZFS snapshots."""

    @pytest.mark.zfs
    def test_claude_code_to_native(self, fleet_orchestrator: FlexAgentClient):
        """MR-CT1: Claude Code -> snapshot -> Native agent picks up files."""
        _skip_without_binary("CLAUDE_CODE_BINARY", "Claude Code")
        client = fleet_orchestrator

        # Claude Code writes files
        resp1 = client.create_session(
            placement="agent-sandbox",
            agent_type="claude-code",
        )
        session1 = resp1["session_id"]
        try:
            collect_events(
                client, session1,
                "Write 'cross-runtime-transfer-data' to /workspace/transfer.txt"
            )
            client.create_snapshot(session1, "transfer-snap")
        finally:
            client.destroy_session(session1)

        # Native agent picks up from snapshot
        resp2 = client.create_session(
            placement="tools-sandbox",
            base_snapshot="transfer-snap",
        )
        session2 = resp2["session_id"]
        try:
            events = collect_events(
                client, session2,
                "Read /workspace/transfer.txt and tell me its exact contents"
            )
            assert events_contain_text(events, "cross-runtime-transfer-data"), (
                "Expected native agent to read Claude Code's file from snapshot"
            )
        finally:
            client.destroy_session(session2)

    @pytest.mark.zfs
    def test_native_to_claude_code(self, fleet_orchestrator: FlexAgentClient):
        """MR-CT2: Native agent -> snapshot -> Claude Code picks up files."""
        _skip_without_binary("CLAUDE_CODE_BINARY", "Claude Code")
        client = fleet_orchestrator

        # Native agent writes files
        resp1 = client.create_session(placement="tools-sandbox")
        session1 = resp1["session_id"]
        try:
            collect_events(
                client, session1,
                "Write 'native-to-cc-data' to /workspace/native-transfer.txt"
            )
            client.create_snapshot(session1, "native-snap")
        finally:
            client.destroy_session(session1)

        # Claude Code picks up from snapshot
        resp2 = client.create_session(
            placement="agent-sandbox",
            agent_type="claude-code",
            base_snapshot="native-snap",
        )
        session2 = resp2["session_id"]
        try:
            events = collect_events(
                client, session2,
                "Read /workspace/native-transfer.txt and tell me its contents"
            )
            assert events_contain_text(events, "native-to-cc-data"), (
                "Expected Claude Code to read native agent's file from snapshot"
            )
        finally:
            client.destroy_session(session2)
