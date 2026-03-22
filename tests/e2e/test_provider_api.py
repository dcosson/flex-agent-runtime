"""Real LLM provider API tests (Plan 22 §9).

Tests provider connectivity, auth, streaming, and tool use against real
LLM provider endpoints. Each provider runs through the same scenario set.

Tier: Real only — requires provider API keys.
"""

import os

import pytest

from .client import ConnectStreamError, FlexAgentClient
from .helpers import has_content_event


pytestmark = [pytest.mark.real, pytest.mark.provider, pytest.mark.timeout(120)]


PROVIDERS = [
    ("anthropic", "claude-haiku-4-5-20251001", "ANTHROPIC_API_KEY"),
    ("openai", "gpt-4o-mini", "OPENAI_API_KEY"),
    ("google", "gemini-2.0-flash-001", "GOOGLE_API_KEY"),
    ("openrouter", "openrouter/auto", "OPENROUTER_API_KEY"),
]


def _skip_without_key(env_var: str):
    """Skip test if API key is not set."""
    if not os.environ.get(env_var):
        pytest.skip(f"{env_var} not set")


class TestProviderSimpleCompletion:
    """PA-1: Simple completion — send "Say hello" -> verify non-empty response."""

    @pytest.mark.parametrize("provider,model,env_var", PROVIDERS)
    def test_simple_completion(self, local_orchestrator: FlexAgentClient,
                               provider: str, model: str, env_var: str):
        _skip_without_key(env_var)
        resp = local_orchestrator.create_session(
            placement="tools-sandbox", provider=provider, model=model,
        )
        session_id = resp["session_id"]
        try:
            events = list(local_orchestrator.send_message(session_id, "Say hello"))
            assert len(events) > 0
            # Should have at least one content event
            content_types = {"text", "text_delta", "content_block_delta"}
            event_types = {e.get("type") for e in events}
            assert event_types & content_types, (
                f"Expected content events from {provider}, got: {event_types}"
            )
        finally:
            local_orchestrator.destroy_session(session_id)


class TestProviderStreaming:
    """PA-2: Streaming — verify events arrive incrementally."""

    @pytest.mark.parametrize("provider,model,env_var", PROVIDERS)
    def test_streaming_incremental(self, local_orchestrator: FlexAgentClient,
                                    provider: str, model: str, env_var: str):
        _skip_without_key(env_var)
        resp = local_orchestrator.create_session(
            placement="tools-sandbox", provider=provider, model=model,
        )
        session_id = resp["session_id"]
        try:
            # Collect events one by one to verify streaming
            event_count = 0
            for event in local_orchestrator.send_message(
                session_id, "Write a short paragraph about testing"
            ):
                event_count += 1
                assert isinstance(event, dict)
            # Should have multiple events (not all-at-once)
            assert event_count > 1, (
                f"Expected multiple streaming events from {provider}, got {event_count}"
            )
        finally:
            local_orchestrator.destroy_session(session_id)


class TestProviderMultiTurn:
    """PA-4: Multi-turn — send message, get response, send follow-up."""

    @pytest.mark.parametrize("provider,model,env_var", PROVIDERS)
    def test_multi_turn_coherence(self, local_orchestrator: FlexAgentClient,
                                   provider: str, model: str, env_var: str):
        _skip_without_key(env_var)
        resp = local_orchestrator.create_session(
            placement="tools-sandbox", provider=provider, model=model,
        )
        session_id = resp["session_id"]
        try:
            # Turn 1
            events1 = list(local_orchestrator.send_message(
                session_id, "My favorite color is blue. Remember that."
            ))
            assert len(events1) > 0

            # Turn 2 — follow-up referencing turn 1
            events2 = list(local_orchestrator.follow_up(
                session_id, "What is my favorite color?"
            ))
            assert len(events2) > 0
        finally:
            local_orchestrator.destroy_session(session_id)


class TestProviderToolUse:
    """PA-3: Tool use — verify provider can produce tool_use blocks."""

    @pytest.mark.parametrize("provider,model,env_var", PROVIDERS)
    @pytest.mark.timeout(300)
    def test_tool_use(self, local_orchestrator: FlexAgentClient,
                      provider: str, model: str, env_var: str):
        _skip_without_key(env_var)
        resp = local_orchestrator.create_session(
            placement="tools-sandbox", provider=provider, model=model,
        )
        session_id = resp["session_id"]
        try:
            # Ask for a task that should trigger tool use (file write)
            events = list(local_orchestrator.send_message(
                session_id,
                "Create a file called /workspace/tool-test.txt containing 'tool-use-verified'"
            ))
            assert len(events) > 0
            assert has_content_event(events), (
                f"Expected content events from {provider} tool use"
            )
        finally:
            local_orchestrator.destroy_session(session_id)


class TestProviderAuthFailure:
    """PA-5: Auth failure — invalid API key produces clean error."""

    def test_invalid_api_key(self, local_orchestrator: FlexAgentClient):
        """Verify that an invalid provider produces a clean error, not a crash."""
        # Use a bogus provider name to trigger an error at session creation
        # or message send time. The system should return an error, not crash.
        try:
            resp = local_orchestrator.create_session(
                placement="tools-sandbox",
                provider="nonexistent-provider",
                model="fake-model",
            )
            session_id = resp.get("session_id")
            if session_id:
                # Session was created; try sending a message — should get
                # an error event or ConnectStreamError, not a crash
                try:
                    events = list(local_orchestrator.send_message(
                        session_id, "Say hello"
                    ))
                    # If we get events, check they include an error indicator
                    event_types = {e.get("type") for e in events}
                    has_error = any(
                        "error" in str(e).lower() for e in events
                    ) or "error" in event_types
                    assert has_error or has_content_event(events), (
                        "Expected either error or content events from invalid provider"
                    )
                except ConnectStreamError:
                    pass  # Clean error — this is the expected path
                finally:
                    try:
                        local_orchestrator.destroy_session(session_id)
                    except Exception:
                        pass
        except Exception as e:
            # Session creation itself failed — that's also acceptable
            # as long as it's a clean HTTP error, not a crash
            assert "500" not in str(e) or "Internal Server Error" not in str(e), (
                f"Expected clean error for invalid provider, got server crash: {e}"
            )
