"""Mock tier deployment mode tests (Plan 22 §4.3).

Tests configs C1 (dev/local) and C2 (tools-sandbox) using stubserver LLM backend.
Verifies orchestrator wiring, RPC transport, session lifecycle, and health monitoring.

Tests DM-M1 through DM-M8.
"""

import time

import pytest

from .client import FlexAgentClient


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

            # Verify we got at least one event with expected structure
            event_types = {e.get("type") for e in events}
            assert len(event_types) > 0

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
        resp = requests.get("http://localhost:18080/health", timeout=5)
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
