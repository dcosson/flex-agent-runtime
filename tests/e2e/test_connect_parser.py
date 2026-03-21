"""Tests for the Connect streaming envelope parser.

Validates correct handling of:
1. Successful multi-message streams
2. Mid-stream errors (ConnectStreamError)
3. Empty streams (just end-of-stream)
4. Truncated streams (EOF mid-envelope)
"""

import io
import json
import struct

import pytest

from .client import ConnectStreamError, _parse_connect_stream, _read_exact


def _make_envelope(flags: int, payload: dict) -> bytes:
    """Build a single Connect envelope frame."""
    data = json.dumps(payload).encode()
    return struct.pack(">BI", flags, len(data)) + data


def _make_end_stream(error: dict | None = None) -> bytes:
    """Build an end-of-stream envelope (flags=0x02)."""
    payload = {}
    if error is not None:
        payload["error"] = error
    return _make_envelope(0x02, payload)


class FakeResponse:
    """Minimal stand-in for a requests.Response with a raw stream."""

    def __init__(self, data: bytes):
        self.raw = io.BytesIO(data)


class TestParseConnectStream:
    def test_successful_multi_message_stream(self):
        """Parse 2 messages + clean end-of-stream."""
        msg1 = {"type": "text_delta", "text": "Hello"}
        msg2 = {"type": "turn_completed", "turn_id": "t1"}
        data = (
            _make_envelope(0x00, msg1)
            + _make_envelope(0x00, msg2)
            + _make_end_stream()
        )
        resp = FakeResponse(data)
        messages = list(_parse_connect_stream(resp))
        assert messages == [msg1, msg2]

    def test_mid_stream_error(self):
        """Parse 1 message then error envelope → yields message, raises error."""
        msg1 = {"type": "text_delta", "text": "Hello"}
        error = {"code": "internal", "message": "provider timeout"}
        data = _make_envelope(0x00, msg1) + _make_end_stream(error)
        resp = FakeResponse(data)
        it = _parse_connect_stream(resp)
        assert next(it) == msg1
        with pytest.raises(ConnectStreamError, match="provider timeout"):
            next(it)

    def test_empty_stream(self):
        """Just an end-of-stream envelope → yields nothing, no error."""
        data = _make_end_stream()
        resp = FakeResponse(data)
        messages = list(_parse_connect_stream(resp))
        assert messages == []

    def test_clean_eof_no_envelopes(self):
        """Completely empty response body → yields nothing."""
        resp = FakeResponse(b"")
        messages = list(_parse_connect_stream(resp))
        assert messages == []

    def test_truncated_header(self):
        """EOF after partial header → raises RuntimeError."""
        # Only 3 bytes of a 5-byte header
        resp = FakeResponse(b"\x00\x00\x00")
        with pytest.raises(RuntimeError, match="Unexpected EOF"):
            list(_parse_connect_stream(resp))

    def test_truncated_payload(self):
        """Header says 100 bytes but only 10 available → raises RuntimeError."""
        header = struct.pack(">BI", 0x00, 100)
        resp = FakeResponse(header + b"x" * 10)
        with pytest.raises(RuntimeError, match="Unexpected EOF"):
            list(_parse_connect_stream(resp))

    def test_error_with_details(self):
        """Error envelope with details array → captured in exception."""
        error = {
            "code": "resource_exhausted",
            "message": "rate limited",
            "details": [{"type": "retry_info", "retry_delay": "5s"}],
        }
        data = _make_end_stream(error)
        resp = FakeResponse(data)
        with pytest.raises(ConnectStreamError) as exc_info:
            list(_parse_connect_stream(resp))
        assert exc_info.value.code == "resource_exhausted"
        assert len(exc_info.value.details) == 1

    def test_single_message_no_end_stream(self):
        """One message then clean EOF (no end-of-stream envelope) → yields message."""
        msg = {"type": "text_delta", "text": "partial"}
        data = _make_envelope(0x00, msg)
        resp = FakeResponse(data)
        messages = list(_parse_connect_stream(resp))
        assert messages == [msg]

    def test_end_stream_with_metadata(self):
        """End-of-stream with metadata but no error → clean end."""
        end = {"metadata": {"request_id": "abc123"}}
        data = _make_envelope(0x02, end)
        resp = FakeResponse(data)
        messages = list(_parse_connect_stream(resp))
        assert messages == []


class TestReadExact:
    def test_reads_exact_bytes(self):
        raw = io.BytesIO(b"hello world")
        assert _read_exact(raw, 5) == b"hello"
        assert _read_exact(raw, 6) == b" world"

    def test_returns_none_on_eof(self):
        raw = io.BytesIO(b"")
        assert _read_exact(raw, 5) is None

    def test_raises_on_partial_read(self):
        raw = io.BytesIO(b"hi")
        with pytest.raises(RuntimeError, match="Unexpected EOF"):
            _read_exact(raw, 5)
