"""Pytest configuration and fixtures for E2E tests.

Provides:
- flexagent_binary: session-scoped path to flexagent binary
- local_orchestrator: module-scoped FlexAgentClient for local mode (Mock tier ports)
- fleet_orchestrator: module-scoped FlexAgentClient for fleet mode (Real tier ports)
- stubserver_orchestrator: module-scoped FlexAgentClient with stubserver LLM backend
- cleanup_leaked_sessions: module-scoped auto-cleanup of leaked sessions
- credential_manager: session-scoped credential loader
"""

import os
import shutil

import pytest
import requests

from .client import FlexAgentClient
from .credentials import CredentialManager
from .process import ProcessManager, wait_for_health


# --- Pytest markers ---

def pytest_configure(config):
    config.addinivalue_line("markers", "real: requires real infrastructure (not run in CI)")
    config.addinivalue_line("markers", "zfs: requires ZFS storage backend")
    config.addinivalue_line("markers", "fleet: requires multiple sandbox-hosts")
    config.addinivalue_line("markers", "multi_runtime: requires Claude Code or Codex binary")
    config.addinivalue_line("markers", "provider: requires real LLM provider API key")


# --- Session-scoped fixtures ---

@pytest.fixture(scope="session")
def flexagent_binary():
    """Path to the flexagent binary."""
    binary = os.environ.get("FLEXAGENT_BINARY", "flexagent")
    assert shutil.which(binary), f"flexagent binary not found: {binary}"
    return binary


@pytest.fixture(scope="session")
def credential_manager():
    """Session-scoped credential loader."""
    return CredentialManager()


# --- Module-scoped orchestrator fixtures ---

@pytest.fixture(scope="module")
def local_orchestrator(flexagent_binary):
    """Start orchestrator + sandbox-host in local mode.

    Uses Mock tier ports (18080/18082).
    """
    pm = ProcessManager(flexagent_binary)
    auth_token = os.environ.get("FLEXAGENT_AUTH_TOKEN", "test-token")

    pm.start_sandbox_host(listen=":18082", FLEXAGENT_AUTH_TOKEN=auth_token)
    wait_for_health("http://localhost:18082/health")

    pm.start_orchestrator(
        listen=":18080",
        sandbox_host_addr="localhost:18082",
        auth_token=auth_token,
    )
    wait_for_health("http://localhost:18080/health")

    client = FlexAgentClient("http://localhost:18080", auth_token=auth_token)
    yield client

    pm.stop_all()


@pytest.fixture(scope="module")
def fleet_orchestrator(flexagent_binary):
    """Start orchestrator + 2 sandbox-hosts in fleet mode.

    Uses Real tier ports (28080/28082/28083). Real tier only.
    """
    pm = ProcessManager(flexagent_binary)
    auth_token = os.environ.get("FLEXAGENT_AUTH_TOKEN", "test-token")

    pm.start_sandbox_host(listen=":28082", FLEXAGENT_AUTH_TOKEN=auth_token)
    pm.start_sandbox_host(listen=":28083", FLEXAGENT_AUTH_TOKEN=auth_token)
    wait_for_health("http://localhost:28082/health")
    wait_for_health("http://localhost:28083/health")

    pm.start_orchestrator(
        listen=":28080",
        sandbox_host_addr="localhost:28082,localhost:28083",
        auth_token=auth_token,
    )
    wait_for_health("http://localhost:28080/health")

    client = FlexAgentClient("http://localhost:28080", auth_token=auth_token)
    yield client

    pm.stop_all()


@pytest.fixture(scope="module")
def stubserver_orchestrator(flexagent_binary):
    """Orchestrator with stubserver as LLM backend.

    Uses Mock tier ports (18080/18082/19090).
    """
    pm = ProcessManager(flexagent_binary)
    auth_token = "test-token"

    pm.start_stubserver(listen=":19090")
    wait_for_health("http://localhost:19090/health")

    pm.start_sandbox_host(listen=":18082", FLEXAGENT_AUTH_TOKEN=auth_token)
    wait_for_health("http://localhost:18082/health")

    pm.start_orchestrator(
        listen=":18080",
        sandbox_host_addr="localhost:18082",
        auth_token=auth_token,
        env={
            "ANTHROPIC_BASE_URL": "http://localhost:19090",
            "FLEXAGENT_AUTH_TOKEN": auth_token,
        },
    )
    wait_for_health("http://localhost:18080/health")

    client = FlexAgentClient("http://localhost:18080", auth_token=auth_token)
    yield client

    pm.stop_all()


# --- Cleanup fixtures ---

@pytest.fixture(scope="module", autouse=True)
def cleanup_leaked_sessions(request):
    """Sweep and destroy any leaked sessions after all tests in a module.

    This fixture depends on whichever orchestrator fixture is active for the
    module. It runs before orchestrator teardown (reverse dependency order).
    Handles connection errors gracefully — the orchestrator may already be
    shutting down if a prior fixture failed.
    """
    yield
    # Try to find the active orchestrator client from the module's fixtures
    for fixture_name in ("local_orchestrator", "fleet_orchestrator", "stubserver_orchestrator"):
        client = request.getfixturevalue(fixture_name) if fixture_name in request.fixturenames else None
        if client is not None:
            try:
                sessions = client.list_sessions()
                for s in sessions.get("sessions", []):
                    try:
                        client.destroy_session(s["session_id"])
                    except Exception:
                        pass
            except (requests.ConnectionError, requests.Timeout):
                pass  # orchestrator already gone
            except Exception:
                pass
            break
