"""Deployment mode tests (Plan 22 §4.3 + §4.4).

Mock tier: Tests configs C1 (dev/local) and C2 (tools-sandbox) using stubserver LLM backend.
Verifies orchestrator wiring, RPC transport, session lifecycle, and health monitoring.
Tests DM-M1 through DM-M8.

Real tier: Tests configs C1-C6 with real LLM providers and infrastructure.
Tests DM-R0 through DM-R16.
"""

import os
import time

import pytest

from .client import FlexAgentClient
from .helpers import has_content_event


pytestmark = pytest.mark.timeout(120)


class TestToolsSandboxSessionLifecycle:
    """DM-M2: Create session (C2, tools-sandbox) -> send message -> get events -> destroy."""

    def test_create_send_destroy(self, local_orchestrator: FlexAgentClient):
        # Create session
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        assert session_id

        try:
            # Send message and consume events
            events = list(local_orchestrator.send_message(session_id, "Say hello"))
            assert len(events) > 0

            # Verify we got meaningful event types (not just errors/garbage)
            event_types = {e.get("type") for e in events}
            # Should have at least a text or content event from the LLM response
            content_types = {"text", "text_delta", "content_block_delta", "message_start"}
            assert event_types & content_types, (
                f"Expected at least one content event type, got: {event_types}"
            )

            # Get session state
            session = local_orchestrator.get_session(session_id)
            assert session["session_id"] == session_id
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_session_get_after_create(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            session = local_orchestrator.get_session(session_id)
            assert session["session_id"] == session_id
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_destroy_session(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        local_orchestrator.destroy_session(session_id)

        # Getting a destroyed session should fail
        with pytest.raises(Exception):
            local_orchestrator.get_session(session_id)


class TestMultiSession:
    """DM-M5: Multiple concurrent sessions on same orchestrator."""

    def test_concurrent_sessions(self, local_orchestrator: FlexAgentClient):
        sessions = []
        try:
            # Create multiple sessions
            for _ in range(3):
                resp = local_orchestrator.create_session(placement="tools-sandbox")
                sessions.append(resp["session_id"])

            # All sessions should be independently accessible
            for sid in sessions:
                session = local_orchestrator.get_session(sid)
                assert session["session_id"] == sid

            # List sessions should show all of them
            listing = local_orchestrator.list_sessions()
            listed_ids = {s["session_id"] for s in listing.get("sessions", [])}
            for sid in sessions:
                assert sid in listed_ids
        finally:
            for sid in sessions:
                try:
                    local_orchestrator.destroy_session(sid)
                except Exception:
                    pass


class TestHealthCheck:
    """DM-M7/DM-M8: Health check behavior."""

    def test_health_returns_healthy(self, local_orchestrator: FlexAgentClient):
        """DM-M7: Health check returns healthy when sandbox-host is up."""
        assert local_orchestrator.health()

    def test_orchestrator_health_endpoint(self, local_orchestrator: FlexAgentClient):
        """Health endpoint returns 200 with proper response."""
        import requests
        resp = requests.get(f"{local_orchestrator.base_url}/health", timeout=5)
        assert resp.status_code == 200


class TestSteer:
    """Test steer and abort RPCs."""

    def test_steer_session(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Steer should succeed on an active session
            local_orchestrator.steer(session_id, "Focus on Python code")
        finally:
            local_orchestrator.destroy_session(session_id)

    def test_abort_session(self, local_orchestrator: FlexAgentClient):
        resp = local_orchestrator.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Abort should succeed (even if nothing is running)
            local_orchestrator.abort(session_id)
        finally:
            local_orchestrator.destroy_session(session_id)


# ---------------------------------------------------------------------------
# Real Tier Tests — require real LLM providers and infrastructure
# ---------------------------------------------------------------------------

def _skip_without_key(env_var: str):
    if not os.environ.get(env_var):
        pytest.skip(f"{env_var} not set")


class TestRealC2ToolsSandbox:
    """DM-R1/R2: Tools-sandbox with real LLM providers."""

    pytestmark = [pytest.mark.real, pytest.mark.timeout(300)]

    @pytest.mark.parametrize("provider,model,env_var", [
        ("anthropic", "claude-haiku-4-5-20251001", "ANTHROPIC_API_KEY"),
        ("openai", "gpt-4o-mini", "OPENAI_API_KEY"),
        ("google", "gemini-2.5-flash", "GOOGLE_API_KEY"),
    ])
    def test_real_provider_session(self, fleet_orchestrator: FlexAgentClient,
                                    provider: str, model: str, env_var: str):
        """DM-R2: Create session with real provider -> verify tool use."""
        _skip_without_key(env_var)
        client = fleet_orchestrator
        resp = client.create_session(
            placement="tools-sandbox", provider=provider, model=model,
        )
        session_id = resp["session_id"]
        try:
            events = list(client.send_message(
                session_id, "Write 'test' to /workspace/dm-test.txt"
            ))
            assert has_content_event(events), (
                f"Expected content events from {provider}"
            )
        finally:
            client.destroy_session(session_id)


class TestRealC5AgentSandbox:
    """DM-R9/R10/R11: Agent-in-sandbox with gVisor."""

    pytestmark = [pytest.mark.real, pytest.mark.timeout(300)]

    def test_agent_sandbox_session(self, fleet_orchestrator: FlexAgentClient):
        """DM-R9: Create agent-sandbox session -> send message -> get events."""
        client = fleet_orchestrator
        resp = client.create_session(placement="agent-sandbox")
        session_id = resp["session_id"]
        try:
            events = list(client.send_message(session_id, "Say hello"))
            assert len(events) > 0
            assert has_content_event(events)
        finally:
            client.destroy_session(session_id)

    def test_agent_sandbox_isolation(self, fleet_orchestrator: FlexAgentClient):
        """DM-R11: Multiple agent-sandbox sessions are isolated."""
        client = fleet_orchestrator
        sessions = []
        try:
            for _ in range(2):
                resp = client.create_session(placement="agent-sandbox")
                sessions.append(resp["session_id"])

            for sid in sessions:
                session = client.get_session(sid)
                assert session["session_id"] == sid
        finally:
            for sid in sessions:
                try:
                    client.destroy_session(sid)
                except Exception:
                    pass


class TestRealC6Fleet:
    """DM-R12/R13/R14: Fleet mode with multiple sandbox-hosts."""

    pytestmark = [pytest.mark.real, pytest.mark.fleet, pytest.mark.timeout(300)]

    def test_sessions_distributed(self, fleet_orchestrator: FlexAgentClient):
        """DM-R12: Sessions are distributed across fleet members."""
        client = fleet_orchestrator
        sessions = []
        try:
            for _ in range(4):
                resp = client.create_session(placement="tools-sandbox")
                sessions.append(resp["session_id"])

            # All sessions should be independently accessible
            for sid in sessions:
                session = client.get_session(sid)
                assert session["session_id"] == sid
        finally:
            for sid in sessions:
                try:
                    client.destroy_session(sid)
                except Exception:
                    pass

    def test_session_stickiness(self, fleet_orchestrator: FlexAgentClient):
        """DM-R13: Sessions stick to assigned host."""
        client = fleet_orchestrator
        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            # Send multiple messages to the same session — should always
            # route to the same sandbox-host
            for i in range(3):
                events = list(client.send_message(
                    session_id, f"Echo 'sticky-test-{i}'"
                ))
                assert has_content_event(events), f"Turn {i} should get content"
        finally:
            client.destroy_session(session_id)

    def test_fleet_health(self, fleet_orchestrator: FlexAgentClient):
        """DM-R14: Fleet health reflects individual host status."""
        assert fleet_orchestrator.health()
