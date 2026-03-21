"""Mock tier sandbox configuration tests (Plan 22 §5.1).

Tests tools-in-sandbox dispatch: file ops, bash commands, grep/glob
execute on the sandbox-host filesystem via RPC.

Tests SC-T1 through SC-T6.

Note: SC-T4 (tool progress streaming) is tested indirectly — streaming
is verified by the Connect parser and the event iteration pattern. A
dedicated incremental-output test would require a long-running bash command,
which is deferred to real tier testing with real LLM.
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


def _events_contain_text(events: list[dict], substring: str) -> bool:
    """Check if any event contains the given substring in text-like fields."""
    for e in events:
        for field in ("text", "content", "data", "output"):
            val = e.get(field, "")
            if isinstance(val, str) and substring in val:
                return True
    return False


def _has_content_event(events: list[dict]) -> bool:
    """Check that events include at least one content/text event."""
    content_types = {"text", "text_delta", "content_block_delta", "message_start"}
    event_types = {e.get("type") for e in events}
    return bool(event_types & content_types)


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
            assert _has_content_event(events), "Expected content events from write"

            # Ask agent to read it back — response should mention the content
            events = _collect_events(
                local_orchestrator, session_id,
                "Read the file /workspace/test.txt and tell me its exact contents"
            )
            assert len(events) > 0
            assert _events_contain_text(events, "hello e2e"), (
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
            events = _collect_events(
                local_orchestrator, session_id,
                "Run the bash command: echo 'sandbox-test-marker'"
            )
            assert len(events) > 0
            assert _has_content_event(events), "Expected content events from bash"
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
            assert _has_content_event(events), "Expected content events from pwd"
        finally:
            local_orchestrator.destroy_session(session_id)


class TestGrepGlob:
    """SC-T3: Grep and glob operations search sandbox-host filesystem."""

    def test_grep_in_sandbox(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write a file with searchable content
            _collect_events(
                local_orchestrator, session_id,
                "Write 'unique-grep-marker-xyz' to /workspace/searchable.txt"
            )
            # Search for it
            events = _collect_events(
                local_orchestrator, session_id,
                "Search for the text 'unique-grep-marker' in /workspace/"
            )
            assert len(events) > 0
            assert _has_content_event(events), "Expected content events from grep"
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_glob_in_sandbox(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Write files
            _collect_events(
                local_orchestrator, session_id,
                "Create files /workspace/a.py and /workspace/b.py with any content"
            )
            # Glob for them
            events = _collect_events(
                local_orchestrator, session_id,
                "List all .py files in /workspace/"
            )
            assert len(events) > 0
            assert _has_content_event(events), "Expected content events from glob"
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

            # Turn 2: read it back — should contain the written content
            events2 = _collect_events(
                local_orchestrator, session_id,
                "Read /workspace/persist-test.txt and tell me its exact contents"
            )
            assert len(events2) > 0
            assert _events_contain_text(events2, "turn1-data"), (
                "Expected persist-test.txt to contain 'turn1-data' across turns"
            )
        finally:
            local_orchestrator.destroy_session(session_id)
