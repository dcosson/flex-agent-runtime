"""Sandbox configuration tests (Plan 22 §5.1, §5.2, §5.3).

Mock tier (§5.1): Tools-in-sandbox dispatch — file ops, bash commands, grep/glob
execute on the sandbox-host filesystem via RPC. Tests SC-T1 through SC-T6.

Real tier (§5.2): Agent-in-sandbox — agent process runs inside gVisor sandbox.
Tests SC-A1 through SC-A5.

Real tier (§5.3): Mixed mode — same orchestrator serves both tools-sandbox and
agent-sandbox sessions. Tests SC-M1, SC-M2.

Note: SC-T4 (tool progress streaming) is tested indirectly — streaming
is verified by the Connect parser and the event iteration pattern. A
dedicated incremental-output test would require a long-running bash command,
which is deferred to real tier testing with real LLM.
"""

import pytest

from .client import FlexAgentClient
from .helpers import collect_events, events_contain_text, has_content_event


pytestmark = pytest.mark.timeout(120)


class TestFileOperations:
    """SC-T1: File operations execute on sandbox-host filesystem."""

    def test_write_and_read_file(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Ask agent to write a file
            events = collect_events(
                local_orchestrator, session_id,
                "Write the text 'hello e2e' to a file called /workspace/test.txt"
            )
            assert len(events) > 0
            assert has_content_event(events), "Expected content events from write"

            # Ask agent to read it back — response should mention the content
            events = collect_events(
                local_orchestrator, session_id,
                "Read the file /workspace/test.txt and tell me its exact contents"
            )
            assert len(events) > 0
            assert events_contain_text(events, "hello e2e"), (
                "Expected read-back to contain 'hello e2e'"
            )
        finally:
            local_orchestrator.destroy_session(session_id)


class TestBashCommands:
    """SC-T2: Bash commands execute in sandbox-host environment."""

    def test_bash_echo(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                local_orchestrator, session_id,
                "Run the bash command: echo 'sandbox-test-marker'"
            )
            assert len(events) > 0
            assert has_content_event(events), "Expected content events from bash"
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_bash_pwd(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                local_orchestrator, session_id,
                "Run pwd and tell me the current directory"
            )
            assert len(events) > 0
            assert has_content_event(events), "Expected content events from pwd"
        finally:
            local_orchestrator.destroy_session(session_id)


class TestGrepGlob:
    """SC-T3: Grep and glob operations search sandbox-host filesystem."""

    def test_grep_in_sandbox(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write a file with searchable content
            collect_events(
                local_orchestrator, session_id,
                "Write 'unique-grep-marker-xyz' to /workspace/searchable.txt"
            )
            # Search for it
            events = collect_events(
                local_orchestrator, session_id,
                "Search for the text 'unique-grep-marker' in /workspace/"
            )
            assert len(events) > 0
            assert has_content_event(events), "Expected content events from grep"
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_glob_in_sandbox(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write files
            collect_events(
                local_orchestrator, session_id,
                "Create files /workspace/a.py and /workspace/b.py with any content"
            )
            # Glob for them
            events = collect_events(
                local_orchestrator, session_id,
                "List all .py files in /workspace/"
            )
            assert len(events) > 0
            assert has_content_event(events), "Expected content events from glob"
        finally:
            local_orchestrator.destroy_session(session_id)


class TestMultiTurnPersistence:
    """SC-T5/SC-T6: Files persist across turns within a session."""

    def test_file_persists_across_turns(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Turn 1: write a file
            events1 = collect_events(
                local_orchestrator, session_id,
                "Write 'turn1-data' to /workspace/persist-test.txt"
            )
            assert len(events1) > 0

            # Turn 2: read it back — should contain the written content
            events2 = collect_events(
                local_orchestrator, session_id,
                "Read /workspace/persist-test.txt and tell me its exact contents"
            )
            assert len(events2) > 0
            assert events_contain_text(events2, "turn1-data"), (
                "Expected persist-test.txt to contain 'turn1-data' across turns"
            )
        finally:
            local_orchestrator.destroy_session(session_id)


# ---------------------------------------------------------------------------
# Real Tier: Agent-in-Sandbox (§5.2) — SC-A1 through SC-A5
# ---------------------------------------------------------------------------

class TestAgentInSandbox:
    """SC-A1/A2/A3/A4/A5: Agent runs inside gVisor sandbox."""

    pytestmark = [pytest.mark.real, pytest.mark.timeout(300)]

    def test_agent_session_in_sandbox(self, fleet_orchestrator: FlexAgentClient):
        """SC-A1: Agent session creates and runs inside sandbox."""
        resp = fleet_orchestrator.create_session(placement="agent-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                fleet_orchestrator, session_id, "Say hello"
            )
            assert len(events) > 0
            assert has_content_event(events), "Expected content from agent-sandbox"
        finally:
            fleet_orchestrator.destroy_session(session_id)

    def test_agent_file_access(self, fleet_orchestrator: FlexAgentClient):
        """SC-A2: Agent can access files within its sandbox mountpoint."""
        resp = fleet_orchestrator.create_session(placement="agent-sandbox")
        session_id = resp["session_id"]
        try:
            collect_events(
                fleet_orchestrator, session_id,
                "Write 'agent-sandbox-data' to /workspace/agent-file.txt"
            )
            events = collect_events(
                fleet_orchestrator, session_id,
                "Read /workspace/agent-file.txt and tell me its contents"
            )
            assert events_contain_text(events, "agent-sandbox-data"), (
                "Expected agent to read file in its sandbox"
            )
        finally:
            fleet_orchestrator.destroy_session(session_id)

    def test_agent_pid_isolation(self, fleet_orchestrator: FlexAgentClient):
        """SC-A3: Agent processes are isolated from host (PID namespace)."""
        resp = fleet_orchestrator.create_session(placement="agent-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                fleet_orchestrator, session_id,
                "Run 'cat /proc/1/cmdline' and tell me what process is PID 1"
            )
            assert has_content_event(events), "Expected PID namespace info"
        finally:
            fleet_orchestrator.destroy_session(session_id)

    def test_agent_cleanup_on_destroy(self, fleet_orchestrator: FlexAgentClient):
        """SC-A4: Agent session cleanup destroys sandbox on destroy."""
        resp = fleet_orchestrator.create_session(placement="agent-sandbox")
        session_id = resp["session_id"]

        # Write something to verify the session is active
        events = collect_events(
            fleet_orchestrator, session_id, "Say hello"
        )
        assert has_content_event(events)

        # Destroy the session
        fleet_orchestrator.destroy_session(session_id)

        # Session should no longer be accessible
        with pytest.raises(Exception):
            fleet_orchestrator.get_session(session_id)

    def test_agent_sessions_isolated(self, fleet_orchestrator: FlexAgentClient):
        """SC-A5: Multiple agent-in-sandbox sessions are isolated."""
        sessions = []
        try:
            for i in range(2):
                resp = fleet_orchestrator.create_session(placement="agent-sandbox")
                sessions.append(resp["session_id"])

            # Write different data in each session
            collect_events(
                fleet_orchestrator, sessions[0],
                "Write 'session-0-data' to /workspace/isolation-test.txt"
            )
            collect_events(
                fleet_orchestrator, sessions[1],
                "Write 'session-1-data' to /workspace/isolation-test.txt"
            )

            # Each session should see its own data
            events0 = collect_events(
                fleet_orchestrator, sessions[0],
                "Read /workspace/isolation-test.txt and tell me its exact contents"
            )
            events1 = collect_events(
                fleet_orchestrator, sessions[1],
                "Read /workspace/isolation-test.txt and tell me its exact contents"
            )

            assert events_contain_text(events0, "session-0-data"), (
                "Session 0 should see its own data"
            )
            assert events_contain_text(events1, "session-1-data"), (
                "Session 1 should see its own data"
            )
        finally:
            for sid in sessions:
                try:
                    fleet_orchestrator.destroy_session(sid)
                except Exception:
                    pass


# ---------------------------------------------------------------------------
# Real Tier: Mixed Mode (§5.3) — SC-M1, SC-M2
# ---------------------------------------------------------------------------

class TestMixedMode:
    """SC-M1/M2: Same orchestrator serves both tools-sandbox and agent-sandbox."""

    pytestmark = [pytest.mark.real, pytest.mark.timeout(300)]

    def test_concurrent_placement_modes(self, fleet_orchestrator: FlexAgentClient):
        """SC-M1: Same orchestrator serves both modes concurrently."""
        resp_tools = fleet_orchestrator.create_session(placement="tools-sandbox")
        resp_agent = fleet_orchestrator.create_session(placement="agent-sandbox")
        tools_sid = resp_tools["session_id"]
        agent_sid = resp_agent["session_id"]

        try:
            # Both sessions should work independently
            events_tools = collect_events(
                fleet_orchestrator, tools_sid, "Say hello"
            )
            events_agent = collect_events(
                fleet_orchestrator, agent_sid, "Say hello"
            )

            assert has_content_event(events_tools), "tools-sandbox should respond"
            assert has_content_event(events_agent), "agent-sandbox should respond"
        finally:
            try:
                fleet_orchestrator.destroy_session(tools_sid)
            except Exception:
                pass
            try:
                fleet_orchestrator.destroy_session(agent_sid)
            except Exception:
                pass

    def test_no_cross_interference(self, fleet_orchestrator: FlexAgentClient):
        """SC-M2: Sessions in different modes don't interfere."""
        resp_tools = fleet_orchestrator.create_session(placement="tools-sandbox")
        resp_agent = fleet_orchestrator.create_session(placement="agent-sandbox")
        tools_sid = resp_tools["session_id"]
        agent_sid = resp_agent["session_id"]

        try:
            # Write distinct data in each
            collect_events(
                fleet_orchestrator, tools_sid,
                "Write 'tools-mode-data' to /workspace/mode-test.txt"
            )
            collect_events(
                fleet_orchestrator, agent_sid,
                "Write 'agent-mode-data' to /workspace/mode-test.txt"
            )

            # Each should see its own data (isolated filesystems)
            events_tools = collect_events(
                fleet_orchestrator, tools_sid,
                "Read /workspace/mode-test.txt and tell me its contents"
            )
            events_agent = collect_events(
                fleet_orchestrator, agent_sid,
                "Read /workspace/mode-test.txt and tell me its contents"
            )

            assert events_contain_text(events_tools, "tools-mode-data")
            assert events_contain_text(events_agent, "agent-mode-data")
        finally:
            try:
                fleet_orchestrator.destroy_session(tools_sid)
            except Exception:
                pass
            try:
                fleet_orchestrator.destroy_session(agent_sid)
            except Exception:
                pass
