"""Credential loading for E2E tests.

Lookup order (later overrides earlier):
1. ~/.flexagent/test-credentials.env (shared secrets file)
2. .env.test.local in repo root (gitignored, per-project overrides)
3. Environment variables (highest precedence)
"""

import os
from pathlib import Path


class CredentialManager:
    """Load test credentials from secrets files and environment."""

    SEARCH_PATHS = [
        Path.home() / ".flexagent" / "test-credentials.env",
        Path.cwd() / ".env.test.local",
    ]

    def __init__(self):
        self.credentials: dict[str, str] = {}
        for path in self.SEARCH_PATHS:
            if path.exists():
                self._load_env_file(path)
        # Environment variables override file-based credentials
        for key in list(self.credentials.keys()):
            if key in os.environ:
                self.credentials[key] = os.environ[key]

    def get(self, key: str, required: bool = False) -> str | None:
        val = self.credentials.get(key) or os.environ.get(key)
        if required and not val:
            raise ValueError(f"Required credential {key} not found")
        return val

    def _load_env_file(self, path: Path):
        for line in path.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            key, _, value = line.partition("=")
            if key and value:
                self.credentials[key.strip()] = value.strip()
