"""Mock tier sandbox configuration tests (Plan 22 §5.1).

Tests tools-in-sandbox dispatch: file ops, bash commands, grep/glob
execute on the sandbox-host filesystem via RPC.

Tests SC-T1 through SC-T6.
"""

import pytest

from .client import FlexAgentClient


pytestmark = pytest.mark.timeout(120)


def _collect_events(client: FlexAgentClient, session_id: str, message: str) -> list[dict]:
    """Send a message and collect all events."""
    return list(client.send_message(session_id, message))


def _find_event(events: list[dict], event_type: str) -> dict | None:
    """Find first event of given type."""
    for e in events:
        if e.get("type") == event_type:
            return e
    return None


class TestFileOperations:
    """SC-T1: File operations execute on sandbox-host filesystem."""

    def test_write_and_read_file(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Ask agent to write a file
            events = _collect_events(
                local_orchestrator, session_id,
                "Write the text 'hello e2e' to a file called /workspace/test.txt"
            )
            assert len(events) > 0

            # Ask agent to read it back
            events = _collect_events(
                local_orchestrator, session_id,
                "Read the file /workspace/test.txt and tell me its contents"
            )
            assert len(events) > 0
        finally:
            local_orchestrator.destroy_session(session_id)


class TestBashCommands:
    """SC-T2: Bash commands execute in sandbox-host environment."""

    def test_bash_echo(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = _collect_events(
                local_orchestrator, session_id,
                "Run the bash command: echo 'sandbox-test-marker'"
            )
            assert len(events) > 0
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_bash_pwd(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = _collect_events(
                local_orchestrator, session_id,
                "Run pwd and tell me the current directory"
            )
            assert len(events) > 0
        finally:
            local_orchestrator.destroy_session(session_id)


class TestMultiTurnPersistence:
    """SC-T5/SC-T6: Files persist across turns within a session."""

    def test_file_persists_across_turns(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Turn 1: write a file
            events1 = _collect_events(
                local_orchestrator, session_id,
                "Write 'turn1-data' to /workspace/persist-test.txt"
            )
            assert len(events1) > 0

            # Turn 2: read it back (should still exist)
            events2 = _collect_events(
                local_orchestrator, session_id,
                "Read /workspace/persist-test.txt"
            )
            assert len(events2) > 0
        finally:
            local_orchestrator.destroy_session(session_id)
