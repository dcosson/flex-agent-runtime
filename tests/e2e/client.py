"""FlexAgent RPC client for E2E tests.

Wraps ConnectRPC's JSON protocol:
- Unary RPCs: POST with application/json, parse single JSON response
- Server-streaming RPCs: POST with application/json request, parse Connect
  envelope-framed responses (5-byte header: flags + big-endian length + payload)
"""

import json
import struct

import requests


class ConnectStreamError(Exception):
    """Raised when a Connect server stream returns an error envelope."""

    def __init__(self, code: str, message: str, details: list | None = None):
        super().__init__(f"Connect error [{code}]: {message}")
        self.code = code
        self.details = details or []


def _read_exact(raw, n: int) -> bytes | None:
    """Read exactly n bytes from a raw stream, or None on clean EOF."""
    buf = b""
    while len(buf) < n:
        chunk = raw.read(n - len(buf))
        if not chunk:
            if len(buf) == 0:
                return None  # clean EOF
            raise RuntimeError(
                f"Unexpected EOF mid-read: got {len(buf)} of {n} bytes"
            )
        buf += chunk
    return buf


def _parse_connect_stream(resp):
    """Parse a Connect streaming response (envelope-framed JSON messages).

    Wire format per envelope:
      [flags: 1 byte] [length: 4 bytes big-endian] [payload: {length} bytes]

    flags=0x00: message envelope (payload is JSON message)
    flags=0x02: end-of-stream envelope (payload has optional "error" and "metadata")
    """
    raw = resp.raw
    while True:
        header = _read_exact(raw, 5)
        if header is None:
            return  # clean EOF (no more envelopes)
        flags, length = struct.unpack(">BI", header)
        payload = _read_exact(raw, length)
        if payload is None:
            raise RuntimeError("Unexpected EOF mid-envelope")
        data = json.loads(payload)
        if flags & 0x02:  # end-of-stream envelope
            if "error" in data:
                err = data["error"]
                raise ConnectStreamError(
                    err.get("code", "unknown"),
                    err.get("message", ""),
                    err.get("details", []),
                )
            return  # clean end-of-stream (may contain trailers)
        yield data


class FlexAgentClient:
    """Thin HTTP client for the orchestrator's ConnectRPC API."""

    def __init__(self, base_url: str, auth_token: str = ""):
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        if auth_token:
            self.session.headers["Authorization"] = f"Bearer {auth_token}"

    def _call(self, procedure: str, payload: dict) -> dict:
        """Unary RPC: POST with JSON body, receive JSON response."""
        url = f"{self.base_url}{procedure}"
        resp = self.session.post(
            url, json=payload, timeout=120,
            headers={"Content-Type": "application/json"},
        )
        resp.raise_for_status()
        return resp.json()

    def _stream(self, procedure: str, payload: dict, timeout: int = 300):
        """Server-streaming RPC via Connect streaming protocol.

        Sends JSON request, receives Connect envelope-framed responses.
        Yields message dicts; raises ConnectStreamError on stream error.
        """
        url = f"{self.base_url}{procedure}"
        resp = self.session.post(
            url, json=payload, stream=True, timeout=timeout,
            headers={"Content-Type": "application/json"},
        )
        resp.raise_for_status()
        yield from _parse_connect_stream(resp)

    # --- Unary RPCs ---

    def create_session(self, **kwargs) -> dict:
        return self._call("/rpc.v1.AgentService/CreateSession", kwargs)

    def get_session(self, session_id: str) -> dict:
        return self._call("/rpc.v1.AgentService/GetSession", {
            "session_id": session_id,
        })

    def destroy_session(self, session_id: str) -> dict:
        return self._call("/rpc.v1.AgentService/DestroySession", {
            "session_id": session_id,
        })

    def steer(self, session_id: str, instruction: str) -> dict:
        return self._call("/rpc.v1.AgentService/Steer", {
            "session_id": session_id, "instruction": instruction,
        })

    def abort(self, session_id: str) -> dict:
        return self._call("/rpc.v1.AgentService/Abort", {
            "session_id": session_id,
        })

    def list_sessions(self) -> dict:
        return self._call("/rpc.v1.AgentService/ListSessions", {})

    def resume_session(self, session_id: str) -> dict:
        return self._call("/rpc.v1.AgentService/ResumeSession", {
            "session_id": session_id,
        })

    def create_snapshot(self, session_id: str, name: str = "") -> dict:
        """Create a ZFS snapshot via the orchestrator (delegates to SandboxService)."""
        return self._call("/rpc.v1.AgentService/CreateSnapshot", {
            "session_id": session_id, "name": name,
        })

    # --- Server-streaming RPCs ---

    def send_message(self, session_id: str, message: str):
        """Server-streaming: returns iterator of event dicts."""
        return self._stream("/rpc.v1.AgentService/SendMessage", {
            "session_id": session_id, "message": message,
        })

    def continue_(self, session_id: str):
        """Server-streaming: continues the agent turn, returns event iterator."""
        return self._stream("/rpc.v1.AgentService/Continue", {
            "session_id": session_id,
        })

    def follow_up(self, session_id: str, message: str):
        """Server-streaming: sends follow-up message, returns event iterator."""
        return self._stream("/rpc.v1.AgentService/FollowUp", {
            "session_id": session_id, "message": message,
        })

    def subscribe_events(self, session_id: str):
        """Server-streaming: returns iterator of event dicts."""
        return self._stream("/rpc.v1.AgentService/SubscribeEvents", {
            "session_id": session_id,
        })

    # --- Health ---

    def health(self) -> bool:
        try:
            resp = self.session.get(f"{self.base_url}/health", timeout=5)
            return resp.status_code == 200
        except requests.RequestException:
            return False
