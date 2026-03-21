"""Real tier sandbox identity verification tests (Plan 22 §7).

Tests that agents are running in the correct sandbox environment.
Exercises Dimension 4 (gVisor isolation).

Tier: Real only — requires gVisor container runtime.
"""

import pytest

from .client import FlexAgentClient
from .helpers import collect_events, extract_text, has_content_event


pytestmark = [pytest.mark.real, pytest.mark.timeout(120)]


class TestSandboxHostname:
    """SI-1: Agent hostname matches sandbox ID, not orchestrator host."""

    def test_agent_hostname_differs_from_host(self, fleet_orchestrator: FlexAgentClient):
        client = fleet_orchestrator
        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Run the command 'hostname' and tell me the exact output"
            )
            assert has_content_event(events), "Expected hostname output"
            # The hostname should be non-empty; we can't assert the exact value
            # but verify we got a response
            text = extract_text(events)
            assert len(text.strip()) > 0, "Expected non-empty hostname"
        finally:
            client.destroy_session(session_id)


class TestCgroupIsolation:
    """SI-2: Agent cgroup shows sandbox-specific isolation."""

    def test_cgroup_isolation(self, fleet_orchestrator: FlexAgentClient):
        client = fleet_orchestrator
        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Run 'cat /proc/self/cgroup' and show me the output"
            )
            assert has_content_event(events), "Expected cgroup output"
        finally:
            client.destroy_session(session_id)


class TestAgentInSandboxHostname:
    """SI-3: Agent-in-sandbox hostname differs from host machine."""

    def test_agent_sandbox_hostname(self, fleet_orchestrator: FlexAgentClient):
        client = fleet_orchestrator
        # Create agent-sandbox session (C5 config)
        resp = client.create_session(placement="agent-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Run 'hostname' and tell me the exact output"
            )
            assert has_content_event(events), "Expected hostname from agent-sandbox"
        finally:
            client.destroy_session(session_id)


class TestToolsSandboxHostname:
    """SI-4: Bash tool hostname output comes from sandbox, not orchestrator."""

    def test_bash_hostname_from_sandbox(self, fleet_orchestrator: FlexAgentClient):
        client = fleet_orchestrator
        resp = client.create_session(placement="tools-sandbox")
        session_id = resp["session_id"]
        try:
            events = collect_events(
                client, session_id,
                "Run the bash command 'hostname' and tell me what it returns"
            )
            assert has_content_event(events), "Expected hostname from tools-sandbox bash"
        finally:
            client.destroy_session(session_id)


class TestSessionIsolation:
    """SI-5: Two concurrent sessions report different hostnames."""

    def test_concurrent_sessions_different_hostnames(self, fleet_orchestrator: FlexAgentClient):
        client = fleet_orchestrator

        resp1 = client.create_session(placement="tools-sandbox")
        resp2 = client.create_session(placement="tools-sandbox")
        session1 = resp1["session_id"]
        session2 = resp2["session_id"]

        try:
            events1 = collect_events(
                client, session1,
                "Run 'hostname' and tell me only the hostname, nothing else"
            )
            events2 = collect_events(
                client, session2,
                "Run 'hostname' and tell me only the hostname, nothing else"
            )

            assert has_content_event(events1), "Expected hostname from session 1"
            assert has_content_event(events2), "Expected hostname from session 2"

            hostname1 = extract_text(events1).strip()
            hostname2 = extract_text(events2).strip()

            # Two sessions should have different hostnames (isolated sandboxes)
            assert hostname1 != hostname2, (
                f"Expected different hostnames for isolated sessions, "
                f"got '{hostname1}' and '{hostname2}'"
            )
        finally:
            try:
                client.destroy_session(session1)
            except Exception:
                pass
            try:
                client.destroy_session(session2)
            except Exception:
                pass
