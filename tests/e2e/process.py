"""Process manager for E2E tests.

Manages flexagent and stubserver processes with:
- Non-interactive execution (stdin=DEVNULL)
- atexit and signal handler cleanup
- Timeout-based shutdown with kill fallback
"""

import atexit
import os
import signal
import subprocess
import sys
import time

import requests


class ProcessManager:
    """Start/stop flexagent processes for testing."""

    def __init__(self, binary_path: str = "flexagent"):
        self.binary = binary_path
        self.processes: list[subprocess.Popen] = []
        atexit.register(self.stop_all)
        signal.signal(signal.SIGTERM, self._signal_handler)
        signal.signal(signal.SIGINT, self._signal_handler)

    def _signal_handler(self, signum, frame):
        self.stop_all()
        sys.exit(128 + signum)

    def start_sandbox_host(self, listen: str = ":8082", **env) -> subprocess.Popen:
        return self._start(["serve", "sandbox-host", "--listen", listen], env)

    def start_orchestrator(
        self, listen: str = ":8080", env: dict | None = None, **kwargs
    ) -> subprocess.Popen:
        args = ["serve", "orchestrator", "--listen", listen]
        for k, v in kwargs.items():
            args.extend([f"--{k.replace('_', '-')}", str(v)])
        return self._start(args, env or {})

    def start_stubserver(
        self,
        binary_path: str = "",
        listen: str = ":19090",
        fixture_dir: str = "testdata/fixtures",
    ) -> subprocess.Popen:
        """Start the stubserver binary (separate from flexagent)."""
        binary = binary_path or os.environ.get("STUBSERVER_BINARY", "stubserver")
        proc = subprocess.Popen(
            [binary, "--listen", listen, "--fixture-dir", fixture_dir],
            env={**os.environ},
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.processes.append(proc)
        return proc

    def _start(self, args: list[str], env: dict) -> subprocess.Popen:
        full_env = {**os.environ, **env}
        proc = subprocess.Popen(
            [self.binary] + args,
            env=full_env,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        self.processes.append(proc)
        return proc

    def stop_all(self, timeout: float = 10):
        for proc in self.processes:
            if proc.poll() is not None:
                continue  # already exited
            proc.terminate()
            try:
                proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
        self.processes.clear()


def wait_for_health(url: str, timeout: float = 30, interval: float = 0.5):
    """Poll health endpoint until it returns 200 or timeout expires."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if requests.get(url, timeout=2).status_code == 200:
                return
        except requests.ConnectionError:
            pass
        time.sleep(interval)
    raise TimeoutError(f"Health check failed after {timeout}s: {url}")
