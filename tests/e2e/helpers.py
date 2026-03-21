"""Shared test helpers for E2E tests."""

from .client import FlexAgentClient


def collect_events(client: FlexAgentClient, session_id: str, message: str) -> list[dict]:
    """Send a message and collect all events."""
    return list(client.send_message(session_id, message))


def has_content_event(events: list[dict]) -> bool:
    """Check that events include at least one content/text event."""
    content_types = {"text", "text_delta", "content_block_delta", "message_start"}
    return bool({e.get("type") for e in events} & content_types)


def events_contain_text(events: list[dict], substring: str) -> bool:
    """Check if any event contains the given substring in text-like fields."""
    for e in events:
        for field in ("text", "content", "data", "output"):
            val = e.get(field, "")
            if isinstance(val, str) and substring in val:
                return True
    return False


def extract_text(events: list[dict]) -> str:
    """Extract all text content from events."""
    parts = []
    for e in events:
        for field in ("text", "content", "data", "output"):
            val = e.get(field, "")
            if isinstance(val, str) and val:
                parts.append(val)
    return "\n".join(parts)
