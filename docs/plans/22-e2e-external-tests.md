# 22: E2E External Test Plan

**Status:** Draft
**Depends on:** 17-external-testing, 21-serve-orchestrator, 18-agent-loop-rpc, 19-ec2-direct-adapter, 20-fleet-management
**Depended on by:** --
**Scope:** Automated end-to-end testing of the full system from an external client perspective. A Python driver script spins up the orchestrator, makes RPC requests, and validates responses across all deployment modes, sandbox configurations, and LLM providers.

---

## 1. Overview

Plan 17 covers Go-level E2E testing that exercises the runtime as a library consumer. This plan covers **external black-box testing** -- exercising the system as a deployed service through its RPC API, the way a real client application would.

The key differences from plan 17:

- **External driver**: Tests are driven from Python (not Go), exercising the full network stack including HTTP transport, ConnectRPC serialization, and auth headers.
- **Deployment modes**: Tests exercise the orchestrator's three deployment configurations (EC2 fleet, EC2 direct, local/sandbox-host) as they would be deployed in production.
- **Real infrastructure**: Tests spin up actual orchestrator and sandbox-host processes, not in-process mocks.
- **Real LLM providers**: Dedicated test suite for API endpoint correctness against Anthropic, OpenAI, Google, and OpenRouter.
- **Cross-runtime**: Tests verify interop between different agent runtimes (Claude Code, Codex).

Non-goals:
- Replacing plan 17's Go-level tests (those remain for fast CI).
- Load testing (covered by plan 16-runtime-test-harness).
- Unit or component testing of internal packages.

---

## 2. Architecture

### 2.1 System Under Test

```mermaid
graph TB
    subgraph "Test Driver (Python)"
        TD[pytest test suite]
        RC[RPC Client<br/>ConnectRPC / HTTP]
        CM[Credential Manager]
    end

    subgraph "System Under Test"
        subgraph "Local Mode"
            ORCH_L[flexagent serve orchestrator<br/>--sandbox-host-addr localhost:8082]
            SH_L[flexagent serve sandbox-host<br/>--listen :8082]
        end

        subgraph "EC2 Direct Mode"
            ORCH_D[flexagent serve orchestrator<br/>--direct-host-addr host:port]
            EC2[EC2 Instance<br/>flexagent serve agent]
        end

        subgraph "EC2 Fleet Mode"
            ORCH_F[flexagent serve orchestrator<br/>--sandbox-host-addr h1:8082,h2:8082]
            SH1[sandbox-host 1]
            SH2[sandbox-host 2]
        end
    end

    TD --> RC
    RC -->|ConnectRPC| ORCH_L
    RC -->|ConnectRPC| ORCH_D
    RC -->|ConnectRPC| ORCH_F
    ORCH_L --> SH_L
    ORCH_D -->|SSM| EC2
    ORCH_F --> SH1
    ORCH_F --> SH2
    CM --> TD

    style TD fill:#e1f5fe
    style RC fill:#e1f5fe
    style CM fill:#e1f5fe
    style ORCH_L fill:#e8f5e9
    style ORCH_D fill:#fff3e0
    style ORCH_F fill:#fce4ec
```

### 2.2 Test Driver Architecture

The test driver is a Python package (`tests/e2e/`) using pytest. It communicates with the orchestrator via HTTP using the ConnectRPC JSON protocol (POST to procedure URLs with JSON bodies). This avoids needing generated protobuf stubs -- ConnectRPC's unary protocol is just `POST /rpc.v1.AgentService/SendMessage` with a JSON body.

```
tests/e2e/
    conftest.py                  # pytest fixtures: orchestrator lifecycle, credentials
    client.py                    # FlexAgentClient: thin RPC client wrapper
    credentials.py               # Credential loading from secrets file
    process.py                   # Process manager: start/stop orchestrator, sandbox-host

    test_deployment_modes.py     # §3: Deployment mode tests
    test_sandbox_config.py       # §4: Sandbox configuration tests
    test_zfs_durability.py       # §5: ZFS durability tests
    test_sandbox_identity.py     # §6: Sandbox identity verification
    test_multi_runtime.py        # §7: Multi-runtime tests
    test_provider_api.py         # §8: Real LLM provider API tests

    requirements.txt             # pytest, requests, (optional) grpcio for streaming)
```

### 2.3 RPC Client

The Python client wraps ConnectRPC's JSON protocol. Unary RPCs use `_call` (POST, parse single JSON response). Server-streaming RPCs (`SendMessage`, `Continue`, `FollowUp`, `SubscribeEvents`) use `_stream` (POST with `stream=True`, parse newline-delimited JSON):

```python
class FlexAgentClient:
    """Thin HTTP client for the orchestrator's ConnectRPC API."""

    def __init__(self, base_url: str, auth_token: str = ""):
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()
        if auth_token:
            self.session.headers["Authorization"] = f"Bearer {auth_token}"
        self.session.headers["Content-Type"] = "application/json"

    def _call(self, procedure: str, payload: dict) -> dict:
        url = f"{self.base_url}{procedure}"
        resp = self.session.post(url, json=payload, timeout=120)
        resp.raise_for_status()
        return resp.json()

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

    def _stream(self, procedure: str, payload: dict, timeout: int = 300):
        """Server-streaming RPC via ConnectRPC streaming protocol.
        Returns an iterator of response dicts (newline-delimited JSON)."""
        url = f"{self.base_url}{procedure}"
        resp = self.session.post(url, json=payload, stream=True, timeout=timeout)
        resp.raise_for_status()
        for line in resp.iter_lines():
            if line:
                yield json.loads(line)

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

    def list_sessions(self) -> dict:
        return self._call("/rpc.v1.AgentService/ListSessions", {})

    def resume_session(self, session_id: str) -> dict:
        return self._call("/rpc.v1.AgentService/ResumeSession", {
            "session_id": session_id,
        })

    def create_snapshot(self, session_id: str, name: str = "") -> dict:
        """Create a ZFS snapshot for the session's sandbox.
        Routes through the orchestrator which delegates to SandboxService."""
        return self._call("/rpc.v1.AgentService/CreateSnapshot", {
            "session_id": session_id, "name": name,
        })

    def subscribe_events(self, session_id: str):
        """Server-streaming: returns iterator of event dicts."""
        return self._stream("/rpc.v1.AgentService/SubscribeEvents", {
            "session_id": session_id,
        })

    def health(self) -> bool:
        resp = self.session.get(f"{self.base_url}/health", timeout=5)
        return resp.status_code == 200
```

For server-streaming RPCs (`SendMessage`, `Continue`, `FollowUp`, `SubscribeEvents`), ConnectRPC uses newline-delimited JSON over a single HTTP response. The `_stream` helper reads lines incrementally and yields parsed dicts. Unary RPCs (`CreateSession`, `GetSession`, `ListSessions`, `ResumeSession`, `Steer`, `Abort`, `DestroySession`, `CreateSnapshot`) use the `_call` helper which returns a single parsed dict.

### 2.4 Process Manager

The test driver manages orchestrator and sandbox-host processes:

```python
class ProcessManager:
    """Start/stop flexagent processes for testing."""

    def __init__(self, binary_path: str = "flexagent"):
        self.binary = binary_path
        self.processes: list[subprocess.Popen] = []

    def start_sandbox_host(self, listen: str = ":8082", **env) -> subprocess.Popen:
        return self._start(["serve", "sandbox-host", "--listen", listen], env)

    def start_orchestrator(self, listen: str = ":8080", **kwargs) -> subprocess.Popen:
        args = ["serve", "orchestrator", "--listen", listen]
        for k, v in kwargs.items():
            args.extend([f"--{k.replace('_', '-')}", str(v)])
        return self._start(args, kwargs.get("env", {}))

    def _start(self, args: list[str], env: dict) -> subprocess.Popen:
        full_env = {**os.environ, **env}
        proc = subprocess.Popen(
            [self.binary] + args,
            env=full_env,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.processes.append(proc)
        return proc

    def start_stubserver(self, binary_path: str = "", listen: str = ":19090",
                         fixture_dir: str = "testdata/fixtures") -> subprocess.Popen:
        """Start the stubserver binary (separate from flexagent)."""
        binary = binary_path or os.environ.get("STUBSERVER_BINARY", "stubserver")
        proc = subprocess.Popen(
            [binary, "--listen", listen, "--fixture-dir", fixture_dir],
            env={**os.environ},
            stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.processes.append(proc)
        return proc

    def stop_all(self):
        for proc in self.processes:
            proc.terminate()
            proc.wait(timeout=10)
        self.processes.clear()
```

---

## 3. Test Safety and Reliability

This is a first-class design concern. These tests will be run by automated agents (not just humans), so every failure mode must resolve cleanly without manual intervention.

### 3.1 Non-Interactive Execution

**Hard rule:** No test step may require interactive input. Every subprocess, API call, and orchestrator command must be fully non-interactive.

- All `flexagent` processes are started with `stdin=/dev/null` (or `subprocess.DEVNULL`).
- No prompts for confirmation, passwords, or API keys at runtime.
- All configuration is via flags, environment variables, or config files -- never stdin.
- The pytest runner itself uses `--no-header -q` to minimize output noise.

### 3.2 Timeouts at Every Layer

Every operation has an explicit timeout. Nothing is allowed to block indefinitely.

| Layer | Timeout | Enforcement |
|-------|---------|-------------|
| **pytest test function** | 120s (default), 300s (infra tests) | `@pytest.mark.timeout(N)` via `pytest-timeout` |
| **HTTP requests** | 30s (unary), 120s (streaming) | `requests.post(..., timeout=N)` |
| **Process startup** | 30s | `wait_for_health()` with deadline |
| **Process shutdown** | 10s | `proc.terminate(); proc.wait(timeout=10)` then `proc.kill()` |
| **LLM API calls** | 60s | HTTP client timeout |
| **Event stream consumption** | 120s | Streaming response timeout |
| **Full test suite** | 600s (fast), 1800s (infra) | pytest `--timeout` global flag |

```python
# Process manager enforces shutdown timeout
def stop_all(self, timeout: float = 10):
    for proc in self.processes:
        proc.terminate()
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)
    self.processes.clear()
```

### 3.3 Automatic Cleanup on Failure

Cleanup must happen whether a test passes, fails, times out, or is interrupted by a signal. The design uses multiple layers of cleanup:

**Layer 1: pytest fixtures with finalizers.** All resource-creating fixtures use `yield` with cleanup in the teardown phase. pytest guarantees teardown runs even on test failure or timeout.

```python
@pytest.fixture
def session(local_orchestrator):
    resp = local_orchestrator.create_session(placement="tools-sandbox")
    session_id = resp["session_id"]
    yield session_id
    # Cleanup: always runs, even on failure
    try:
        local_orchestrator.destroy_session(session_id)
    except Exception:
        pass  # Best-effort cleanup
```

**Layer 2: Process manager with atexit and signal handlers.** The process manager registers cleanup hooks so that even if pytest itself crashes, all child processes are terminated.

```python
class ProcessManager:
    def __init__(self, binary_path: str = "flexagent"):
        self.binary = binary_path
        self.processes: list[subprocess.Popen] = []
        atexit.register(self.stop_all)
        signal.signal(signal.SIGTERM, self._signal_handler)
        signal.signal(signal.SIGINT, self._signal_handler)

    def _signal_handler(self, signum, frame):
        self.stop_all()
        sys.exit(128 + signum)
```

**Layer 3: Module-scoped sweep.** Each test module's fixture teardown enumerates and destroys all sessions that weren't explicitly cleaned up (leaked sessions). This fixture depends on the orchestrator fixture, ensuring the session sweep runs **before** the orchestrator process is stopped (pytest tears down fixtures in reverse dependency order).

```python
@pytest.fixture(scope="module", autouse=True)
def cleanup_leaked_sessions(local_orchestrator):
    yield
    # After all tests in this module: sweep any remaining sessions.
    # This runs before local_orchestrator teardown (reverse dep order).
    # Handle connection errors gracefully — the orchestrator may already
    # be shutting down if a prior fixture failed.
    try:
        sessions = local_orchestrator.list_sessions()
        for s in sessions.get("sessions", []):
            try:
                local_orchestrator.destroy_session(s["session_id"])
            except Exception:
                pass
    except (requests.ConnectionError, requests.Timeout):
        pass  # Orchestrator already gone — nothing to sweep
    except Exception:
        pass
```

**Layer 4: Infrastructure cleanup.** For tests that create EC2 instances or ZFS volumes:

```python
@pytest.fixture(scope="module")
def ec2_cleanup():
    created_instances = []
    yield created_instances
    # Teardown: terminate all EC2 instances created during tests
    for instance_id in created_instances:
        try:
            ec2_client.terminate_instances(InstanceIds=[instance_id])
        except Exception:
            pass
```

### 3.4 No Hanging Agents

The test driver must never block waiting for an agent that has become unresponsive.

- **Event stream reads** use a timeout: if no event arrives within 120s, the stream is closed and the test fails.
- **SendMessage** calls set a client-side timeout. If the orchestrator doesn't respond, the client raises `TimeoutError`.
- **Agent turns** have a maximum duration enforced by the test, not the agent. If a turn takes too long, the test calls `Abort()` then `DestroySession()`.

```python
def wait_for_turn_complete(client, session_id, timeout=120):
    """Consume events until turn completes or timeout."""
    deadline = time.time() + timeout
    for event in client.subscribe_events(session_id):
        if event.get("type") == "turn_completed":
            return event
        if time.time() > deadline:
            client.abort(session_id)
            raise TimeoutError(f"Turn did not complete within {timeout}s")
    raise RuntimeError("Event stream ended without turn_completed")
```

### 3.5 Idempotent Test Runs

Tests must not depend on state from previous runs. Each test module:

1. Starts fresh processes (no reuse of processes from other modules unless explicitly shared via session-scoped fixtures).
2. Creates its own sessions with unique IDs.
3. Cleans up all created resources in teardown.

Port allocation uses a deterministic scheme per test tier to avoid conflicts:

| Tier | Orchestrator Port | Sandbox-Host Ports | Stubserver Port |
|------|-------------------|--------------------|-----------------|
| Mock | 18080 | 18082, 18083 | 19090 |
| Real | 28080 | 28082, 28083 | N/A (real providers) |

This allows the two tiers to run without port conflicts if needed.

---

## 4. Deployment Dimension Test Matrix

Tests are organized around the four deployment dimensions from the README. Each test exercises a specific combination of these dimensions.

### 4.1 The Four Dimensions

| # | Dimension | Values | What It Determines |
|---|-----------|--------|--------------------|
| 1 | **Agent Loop Placement** | Orchestrator, Remote | Where the agent loop process runs |
| 2 | **Tools Placement** | Co-located, Sandbox | Where tool execution happens |
| 3 | **Agent Type** | Native, Terminal (Claude Code, Codex) | What drives the agent loop |
| 4 | **Execution Environment** | Bare instance, gVisor (+ZFS), E2B, Daytona, Fly | Isolation level for the host |

### 4.2 Test Configuration Matrix

Each row is a tested combination. The **Tier** column indicates which test tier covers it.

| Config ID | Agent Loop | Tools | Agent Type | Exec Env | Tier | Infra Setup |
|-----------|-----------|-------|------------|----------|------|-------------|
| **C1** | Orchestrator | Co-located | Native | Bare | Mock | `serve orchestrator` only (dev/local mode) |
| **C2** | Orchestrator | Sandbox | Native | gVisor | Mock + Real | `serve orchestrator` + `serve sandbox-host` (tools-sandbox) |
| **C3** | Remote | Co-located | Terminal (Claude Code) | Bare | Real | `serve orchestrator --direct-host-addr` + EC2/local agent |
| **C4** | Remote | Co-located | Terminal (Codex) | Bare | Real | Same as C3, different agent binary |
| **C5** | Remote | Co-located | Terminal | gVisor | Real | `serve orchestrator` + `serve sandbox-host` (agent-sandbox) |
| **C6** | Orchestrator | Sandbox | Native | gVisor (fleet) | Real | `serve orchestrator --sandbox-host-addr h1,h2` + 2× sandbox-host |

### 4.3 Mock Tier Tests (CI — stubserver LLM)

These tests use the stubserver as the LLM backend. They verify orchestrator wiring, RPC transport, session lifecycle, and tool dispatch — but **not** real LLM behavior, real EC2 provisioning, real ZFS durability, or real agent binaries.

**What the mock tier covers:**
- ConnectRPC transport: request/response serialization, streaming, auth headers
- Session lifecycle: create, send message, get events, destroy
- Health monitoring and error reporting
- Tools-sandbox dispatch: file ops, bash, grep/glob via sandbox-host RPC
- Multi-session concurrency on a single orchestrator
- Orchestrator restart resilience (sandbox-host stays up)

**What the mock tier does NOT cover (real tier only):**
- Real LLM provider API calls (Anthropic, OpenAI, Google, OpenRouter)
- Real EC2 instance provisioning and SSH connectivity
- ZFS snapshot/clone/rollback durability
- Terminal agent binaries (Claude Code, Codex)
- Fleet routing across multiple physical hosts
- Cross-runtime context transfer via ZFS snapshots
- gVisor container isolation verification
- Remote agent process lifecycle

```
Configs tested: C1, C2

Setup (C1 — dev/local):
  1. Start orchestrator in dev mode (no sandbox-host)
  2. Wait for /health

Setup (C2 — tools-sandbox):
  1. Start sandbox-host
  2. Start orchestrator with --sandbox-host-addr localhost:<port>
  3. Wait for /health on both

Tests:
  DM-M1: Create session (C1, dev/local) -> send message -> get events -> destroy
  DM-M2: Create session (C2, tools-sandbox) -> send message -> get events -> destroy
  DM-M3: File write via tools-sandbox -> read back -> verify contents
  DM-M4: Bash command via tools-sandbox -> verify output
  DM-M5: Multiple concurrent sessions on same orchestrator
  DM-M6: Session survives orchestrator restart (if sandbox-host stays up)
  DM-M7: Health check returns healthy when sandbox-host is up
  DM-M8: Health check reflects unhealthy when sandbox-host is down
```

### 4.4 Real Tier Tests (Local machine — real providers, real infra)

These tests use real LLM providers, real infrastructure, and real agent binaries. They run on a developer's local machine (or dedicated test host), not in CI.

```
Configs tested: C2 (real LLM), C3, C4, C5, C6

--- C2 with real LLM providers ---
Setup:
  1. Start sandbox-host with gVisor + ZFS
  2. Start orchestrator with real provider credentials

Tests:
  DM-R1: Create session (tools-sandbox) -> send real coding task -> verify tool use
  DM-R2: Same with each provider (Anthropic, OpenAI, Google, OpenRouter)
  DM-R3: Verify usage/cost tracking in session metadata

--- C3/C4: Remote terminal agents (agent-direct) ---
Setup:
  1. Start remote host running `flexagent serve agent` (EC2 or local)
  2. Start orchestrator with --direct-host-addr <host>:<port>

Tests:
  DM-R4: Create session (agent-direct, Claude Code) -> send message -> get events
  DM-R5: Create session (agent-direct, Codex) -> send message -> get events
  DM-R6: Verify agent runs on remote host (hostname check)
  DM-R7: Multiple sessions on same direct host
  DM-R8: Session cleanup on destroy (no leaked processes)

--- C5: Agent-in-sandbox (gVisor) ---
Setup:
  1. Start sandbox-host with gVisor + ZFS
  2. Start orchestrator with sandbox-host-addr

Tests:
  DM-R9: Create session (agent-sandbox) -> send message -> get events
  DM-R10: Verify agent process runs inside gVisor container (PID namespace)
  DM-R11: Multiple agent-sandbox sessions are isolated from each other

--- C6: Fleet mode ---
Setup:
  1. Start 2 sandbox-hosts on different ports
  2. Start orchestrator with --sandbox-host-addr h1,h2

Tests:
  DM-R12: Sessions distributed across fleet members
  DM-R13: Sessions stick to assigned host
  DM-R14: Fleet health reflects individual host status
  DM-R15: Session creation fails gracefully when all hosts full
  DM-R16: Fleet handles host going down
```

---

## 5. Sandbox Configuration Tests

These tests verify tool dispatch behavior across the **Tools Placement** dimension (co-located vs sandbox).

### 5.1 Tools-in-Sandbox (Dimension 2: Sandbox)

Agent loop runs in orchestrator process; tool calls dispatch to sandbox-host via RPC. Corresponds to configs C2/C6 from §4.2.

```
SC-T1: File operations (read, write, edit) execute on sandbox-host filesystem
SC-T2: Bash commands execute in sandbox-host environment
SC-T3: Grep and glob operations search sandbox-host filesystem
SC-T4: Tool progress streaming works (bash output arrives incrementally)
SC-T5: Agent can create and read files across multiple turns
SC-T6: File edits persist within the session
```

**Tier:** Mock (SC-T1 through SC-T6 use stubserver) + Real (with real LLM for DM-R1/R2)

### 5.2 Agent-in-Sandbox (Dimension 1: Remote + Dimension 4: gVisor)

Agent process runs inside a gVisor sandbox on the sandbox-host. All tools run locally inside the sandbox. Corresponds to config C5 from §4.2.

```
SC-A1: Agent session creates and runs inside sandbox
SC-A2: Agent can access files within its sandbox mountpoint
SC-A3: Agent processes are isolated from host (PID namespace)
SC-A4: Agent session cleanup destroys sandbox on destroy
SC-A5: Multiple agent-in-sandbox sessions are isolated from each other
```

**Tier:** Real only (requires gVisor + real agent binary)

### 5.3 Mixed Mode

```
SC-M1: Same orchestrator serves both tools-sandbox and agent-sandbox sessions concurrently
SC-M2: Sessions in different modes don't interfere with each other
```

**Tier:** Real only (agent-sandbox requires real agent binary)

---

## 6. ZFS Durability Tests

These tests verify data persistence across agent lifecycles using ZFS. Exercises Dimension 4 (Execution Environment: gVisor+ZFS).

**Tier:** Real only
**Prerequisite:** Sandbox-host configured with ZFS storage backend (`SANDBOX_STORAGE_BACKEND=zfs`).

```
ZFS-D1: Write file in session A -> destroy A -> create session B on same ZFS dataset
         -> read file back from B -> verify contents match
         (Note: requires snapshot + clone workflow, not just destroy/recreate)

ZFS-D2: Agent writes file -> create snapshot -> agent writes more -> rollback
         -> verify filesystem matches snapshot state

ZFS-D3: Multi-turn agent: each turn creates a snapshot
         -> verify snapshot count matches turn count
         -> rollback to turn N-1 snapshot -> verify state

ZFS-D4: Pause session -> resume session -> verify files are still present

ZFS-D5: Agent writes large file (100MB) -> snapshot -> destroy -> clone from snapshot
         -> verify large file intact (tests ZFS COW under load)
```

### 6.1 Cross-Agent File Persistence Flow

```mermaid
sequenceDiagram
    participant Test as Python Test Driver
    participant Orch as Orchestrator
    participant SH as Sandbox-Host (ZFS)

    Test->>Orch: CreateSession(session_a, tools-sandbox)
    Orch->>SH: CreateSession RPC
    SH-->>Orch: {session_id, mountpoint}
    Orch-->>Test: {session_id: session_a}

    Test->>Orch: SendMessage(session_a, "Write 'hello' to /workspace/test.txt")
    Note over SH: Agent writes file via tool dispatch
    Orch-->>Test: events (tool_completed)

    Test->>Orch: CreateSnapshot(session_a, "checkpoint-1")
    Orch->>SH: CreateSnapshot RPC
    SH-->>Orch: {snapshot_id}

    Test->>Orch: DestroySession(session_a)

    Test->>Orch: CreateSession(session_b, tools-sandbox, base=checkpoint-1)
    Orch->>SH: CreateSession(base_snapshot=checkpoint-1)
    SH-->>Orch: {session_id, mountpoint}

    Test->>Orch: SendMessage(session_b, "Read /workspace/test.txt")
    Note over SH: Agent reads file from cloned ZFS dataset
    Orch-->>Test: events (file content = "hello")
    Test->>Test: Assert content == "hello"
```

---

## 7. Sandbox Identity Verification

These tests verify agents are running in the correct sandbox environment. Exercises Dimension 4 (gVisor isolation).

**Tier:** Real only

```
SI-1: Agent reads hostname -> verify it matches sandbox ID or container hostname
       (not the orchestrator host)

SI-2: Agent reads /proc/self/cgroup -> verify cgroup isolation
       (gVisor sandbox should show sandbox-specific cgroup)

SI-3: Agent-in-sandbox: hostname differs from host machine hostname

SI-4: Tools-in-sandbox: bash tool `hostname` output comes from sandbox,
       not orchestrator process

SI-5: Two concurrent sessions report different hostnames
       (verifies session isolation)
```

---

## 8. Multi-Runtime Tests

These tests verify interop between different agent driver types. Exercises Dimension 3 (Agent Type: Terminal agents).

**Tier:** Real only

### 8.1 Claude Code Sandbox

```
MR-CC1: Launch Claude Code driver in agent-sandbox mode
         -> verify normalized events stream back
         -> verify tool calls execute in sandbox

MR-CC2: Claude Code agent writes files -> take snapshot
         -> verify snapshot captures Claude Code's changes
```

### 8.2 Codex Sandbox

```
MR-CX1: Launch Codex driver in agent-sandbox mode
         -> verify normalized events stream back

MR-CX2: Codex agent can execute tools inside its sandbox
```

### 8.3 Cross-Runtime Context Transfer

```
MR-CT1: Claude Code agent works on task -> snapshot -> destroy
         -> Create new session with NativeDriver (or another driver)
            based on same snapshot
         -> New agent reads files and continues work
         -> Verify file state is inherited correctly

MR-CT2: NativeDriver agent writes code -> snapshot
         -> Claude Code agent picks up from snapshot
         -> Verify Claude Code can read and modify the files
```

**Note:** Context transfer means filesystem state transfer (via ZFS snapshots), not conversation history transfer. The new agent starts a fresh conversation but inherits the file system state.

### 8.4 Remote ExecutionEnvironment Providers (Optional)

An optional `test_remote_providers.py` module can exercise remote `ExecutionEnvironment` providers (E2B, Daytona, Fly) in nightly runs. These tests verify provider selection, environment factory wiring, and credential routing that are not visible with Native sandbox alone. Requires provider-specific credentials and is nightly-only.

```
MR-RP1: Create session with E2B provider -> verify agent runs in E2B sandbox
MR-RP2: Create session with Fly provider -> verify agent runs in Fly machine
MR-RP3: Verify provider selection based on session config
```

---

## 9. Real LLM Provider API Tests

A focused test suite that exercises real provider API endpoints. These are **not** full agent tests -- they test provider connectivity, auth, and basic streaming at the API level.

**Tier:** Real only

### 9.1 Provider Test Matrix

| Provider | Env Var | Model for Testing | Expected Cost |
|----------|---------|-------------------|---------------|
| Anthropic | `ANTHROPIC_API_KEY` | `claude-haiku-4-5-20251001` | ~$0.001/test |
| OpenAI | `OPENAI_API_KEY` | `gpt-4o-mini` | ~$0.001/test |
| Google | `GOOGLE_API_KEY` | `gemini-2.0-flash` | ~$0.001/test |
| OpenRouter | `OPENROUTER_API_KEY` | (cheapest available) | ~$0.001/test |

### 9.2 Test Scenarios

Each provider runs through the same scenario set:

```
PA-1: Simple completion -- send "Say hello" -> verify non-empty response
PA-2: Streaming -- verify SSE events arrive incrementally (not all at once)
PA-3: Tool use -- send schema + prompt requiring tool call -> verify tool_use block
PA-4: Multi-turn -- send message, get response, send follow-up -> verify coherence
PA-5: Auth failure -- use invalid API key -> verify clean error (not crash)
PA-6: Model lookup -- verify model ID resolves correctly in catalog
```

### 9.3 Provider Tests via Orchestrator

These tests run through the full orchestrator stack with real providers:

```
PA-O1: Create session with real Anthropic provider
        -> send coding task -> verify agent uses tools -> destroy
PA-O2: Same with OpenAI provider
PA-O3: Same with Google provider
PA-O4: Verify usage/cost tracking in session metadata after real API calls
```

### 9.4 Test Structure

```python
@pytest.mark.parametrize("provider,model,env_var", [
    ("anthropic", "claude-haiku-4-5-20251001", "ANTHROPIC_API_KEY"),
    ("openai", "gpt-4o-mini", "OPENAI_API_KEY"),
    ("google", "gemini-2.0-flash", "GOOGLE_API_KEY"),
])
def test_provider_simple_completion(provider, model, env_var):
    api_key = os.environ.get(env_var)
    if not api_key:
        pytest.skip(f"{env_var} not set")
    # ... test implementation
```

---

## 10. Credential Management

### 10.1 Secrets File Convention

Test credentials are stored in a local secrets file that is never committed to git.

**File:** `~/.flexagent/test-credentials.env`

```bash
# LLM Provider API keys
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...
GOOGLE_API_KEY=AIza...
OPENROUTER_API_KEY=sk-or-...

# AWS credentials (for EC2 direct/fleet tests)
AWS_REGION=us-west-2
AWS_ACCESS_KEY_ID=AKIA...
AWS_SECRET_ACCESS_KEY=...

# Orchestrator auth token (for test deployments)
FLEXAGENT_AUTH_TOKEN=test-e2e-token-...
```

### 10.2 Lookup Order

The credential manager loads credentials in this order (later overrides earlier):

1. `~/.flexagent/test-credentials.env` (user's local secrets file)
2. Environment variables (set by CI or shell)
3. `.env.test.local` in the repo root (gitignored, for per-project overrides)

```python
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
            self.credentials[key.strip()] = value.strip()
```

### 10.3 CI Integration

In CI, credentials are injected via GitHub Actions secrets:

```yaml
env:
  ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
  OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
  GOOGLE_API_KEY: ${{ secrets.GOOGLE_API_KEY }}
  FLEXAGENT_AUTH_TOKEN: ${{ secrets.FLEXAGENT_AUTH_TOKEN }}
```

Tests that require credentials skip gracefully when they're not available:

```python
def require_credential(key: str):
    """pytest skip decorator for credential-gated tests."""
    val = os.environ.get(key)
    return pytest.mark.skipif(not val, reason=f"{key} not set")
```

---

## 11. CI Integration

### 11.1 Test Tiers

| Tier | What | Prerequisites | Duration | Where |
|------|------|---------------|----------|-------|
| **Mock** | Configs C1+C2 with stubserver LLM. Verifies orchestrator wiring, RPC transport, session lifecycle, tool dispatch. | `flexagent` + `stubserver` binaries | <5min | CI (every PR) |
| **Real** | All configs (C1-C6) with real LLM providers, real EC2/gVisor/ZFS, real agent binaries. Verifies full end-to-end behavior. | Real infrastructure, API keys, agent binaries, ZFS | <30min | Local machine only (no CI setup) |

### 11.2 CI Workflow (Mock tier only)

```yaml
# .github/workflows/e2e-external.yml
name: E2E External Tests

on:
  pull_request:

jobs:
  build-binaries:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: |
          go build -o flexagent ./cmd/flexagent
          go build -o stubserver ./cmd/stubserver
      - uses: actions/upload-artifact@v4
        with:
          name: flexagent-binaries
          path: |
            flexagent
            stubserver

  e2e-mock:
    name: "E2E Mock: stubserver + local orchestrator"
    runs-on: ubuntu-latest
    needs: build-binaries
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with: { python-version: '3.12' }
      - uses: actions/download-artifact@v4
        with: { name: flexagent-binaries }
      - run: chmod +x flexagent stubserver && pip install -r tests/e2e/requirements.txt
      - run: pytest tests/e2e/ -m "not real" -v --timeout=120
        env:
          FLEXAGENT_BINARY: ./flexagent
          STUBSERVER_BINARY: ./stubserver
          FLEXAGENT_AUTH_TOKEN: test-token
```

The **Real** tier has no CI workflow. It is run manually on a local machine or dedicated test host that has the required infrastructure (ZFS, gVisor, agent binaries, provider API keys, EC2 access):

```bash
# Run all real tier tests locally
pytest tests/e2e/ -m "real" -v --timeout=600
```

### 11.3 Pytest Markers

```python
# conftest.py
import pytest

def pytest_configure(config):
    config.addinivalue_line("markers", "real: requires real infrastructure (not run in CI)")
    config.addinivalue_line("markers", "zfs: requires ZFS storage backend")
    config.addinivalue_line("markers", "fleet: requires multiple sandbox-hosts")
    config.addinivalue_line("markers", "multi_runtime: requires Claude Code or Codex binary")
    config.addinivalue_line("markers", "provider: requires real LLM provider API key")
```

---

## 12. Test Fixtures (conftest.py)

### 12.1 Orchestrator Lifecycle Fixture

```python
@pytest.fixture(scope="session")
def flexagent_binary():
    """Path to the flexagent binary."""
    binary = os.environ.get("FLEXAGENT_BINARY", "flexagent")
    assert shutil.which(binary), f"flexagent binary not found: {binary}"
    return binary

@pytest.fixture(scope="module")
def local_orchestrator(flexagent_binary):
    """Start orchestrator + sandbox-host in local mode for the test module.
    Uses Mock tier ports (18080/18082)."""
    pm = ProcessManager(flexagent_binary)
    auth_token = os.environ.get("FLEXAGENT_AUTH_TOKEN", "test-token")

    # Start sandbox-host on Mock tier port
    pm.start_sandbox_host(listen=":18082", FLEXAGENT_AUTH_TOKEN=auth_token)
    wait_for_health("http://localhost:18082/health")

    # Start orchestrator on Mock tier port
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
    Uses Real tier ports (28080/28082/28083). Real tier only."""
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

def wait_for_health(url: str, timeout: float = 30, interval: float = 0.5):
    """Poll health endpoint until it returns 200."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if requests.get(url, timeout=2).status_code == 200:
                return
        except requests.ConnectionError:
            pass
        time.sleep(interval)
    raise TimeoutError(f"Health check failed: {url}")
```

### 12.2 Stubserver LLM Fixture

For tests that don't need real LLM providers, the test driver starts the stubserver (from plan 17 §12) as the LLM backend:

```python
@pytest.fixture(scope="module")
def stubserver_orchestrator(flexagent_binary):
    """Orchestrator with stubserver as LLM backend.
    Uses Mock tier ports."""
    pm = ProcessManager(flexagent_binary)
    auth_token = "test-token"

    # Start stubserver (separate binary, built alongside flexagent)
    pm.start_stubserver(listen=":19090")
    wait_for_health("http://localhost:19090/health")

    # Start sandbox-host + orchestrator with provider pointed at stubserver
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
```

---

## 13. Acceptance Criteria

| Criterion | Target | How Measured |
|-----------|--------|-------------|
| Mock tier pass rate | 100% on every PR | CI status check |
| Mock tier runtime | <5 minutes | CI job duration |
| Mock tier configs covered | C1 + C2 from §4.2 | Test count per config |
| Real tier configs covered | C1-C6 from §4.2 | Test count per config (local run) |
| Deployment dimension coverage | All 4 dimensions exercised | Dimension × value coverage matrix |
| Provider API coverage | All 4 providers pass basic tests | Provider test results (real tier) |
| ZFS durability | Write/snapshot/clone/read verified | ZFS test assertions (real tier) |
| Multi-runtime | At least 2 driver types tested | Multi-runtime test count (real tier) |
| Credential management | No hardcoded secrets in code | Grep for API keys in committed files |
| CI integration | Mock tier workflow defined and passing | Workflow file exists and parses |

---

## 14. Implementation Sequence

```mermaid
graph LR
    A[Phase 1<br/>Python client +<br/>process manager +<br/>credential manager] --> B[Phase 2<br/>Mock tier tests<br/>CI workflow]
    B --> C[Phase 3<br/>Real tier tests<br/>all configs]

    style A fill:#e8f5e9
    style B fill:#e8f5e9
    style C fill:#fff3e0
```

| Phase | Work | Depends On |
|-------|------|------------|
| **Phase 1** | `client.py`, `process.py`, `credentials.py`, `conftest.py`, `requirements.txt` | `flexagent` + `stubserver` binaries build |
| **Phase 2** | Mock tier: `test_deployment_modes.py` (C1+C2), `test_sandbox_config.py` (tools-sandbox), `.github/workflows/e2e-external.yml` | Phase 1 |
| **Phase 3** | Real tier: fleet mode (C6), ZFS durability, agent-direct (C3/C4), agent-sandbox (C5), provider API tests, multi-runtime, sandbox identity | Phase 2, real infrastructure |

---

## 15. Relationship to Plan 17

| Aspect | Plan 17 (Go E2E) | Plan 22 (External E2E) |
|--------|-------------------|------------------------|
| **Language** | Go | Python |
| **Interface** | Library API (in-process) | RPC API (over network) |
| **LLM backend** | ScriptedProvider / stubserver | Stubserver / real providers |
| **Deployment** | In-process or Docker | Real processes (`flexagent serve`) |
| **Focus** | Agent loop correctness, cross-mode parity | Deployment correctness, infra integration |
| **Speed** | <30s (Tier 1) | <5min (Mock tier) |
| **CI** | Every PR (all tiers) | Every PR (Mock tier only); Real tier local-only |

The two plans are complementary. Plan 17 catches bugs in the agent loop and tool execution logic with fast, deterministic tests. Plan 22 catches deployment, wiring, credential, and infrastructure integration bugs that only surface when running real processes over the network.

---

## 16. Decisions

These were originally open questions, now resolved:

1. **ConnectRPC streaming from Python**: **Decision: manual newline-delimited JSON.** ConnectRPC's server-streaming protocol over HTTP sends newline-delimited JSON objects. The Python client uses `requests` with `stream=True` and `resp.iter_lines()` to parse events incrementally. No `grpcio` dependency needed — the `_stream` helper in `FlexAgentClient` (§2.3) handles this directly. This keeps the test driver lightweight with zero protobuf/gRPC dependencies.

2. **Stubserver as LLM backend**: **Decision: use the Go stubserver binary from Plan 17.** The CI workflow builds both `flexagent` and `stubserver` binaries in the `build-binary` job. The `ProcessManager.start_stubserver()` method handles locating and starting it. This avoids reimplementing fixture serving in Python and keeps the stubserver implementation authoritative in one place.

3. **Port allocation**: **Decision: tier-specific fixed ports from §3.5.** Each test tier uses a unique port range (E2E-Fast: 18xxx, E2E-Standard: 28xxx, E2E-Provider: 38xxx, E2E-Infra: 48xxx). All fixtures use these ports instead of default ports. This allows parallel tier execution without conflicts while keeping port assignment deterministic and debuggable. Dynamic port allocation was rejected as it adds complexity to the process manager and makes debugging harder.

4. **EC2 direct mode without real EC2**: **Recommended default: local simulation is sufficient for CI.** The local `flexagent serve agent` process exercises the same direct-host code path. Real EC2 testing is reserved for nightly E2E-Infra runs. Localstack/mock EC2 is not worth the investment since the direct mode code path is small (just SSH/network setup vs. agent interaction).

5. **Multi-runtime binary availability**: **Recommended default: nightly-only.** Multi-runtime tests require Claude Code and Codex binaries which are not available in standard CI runners. These tests run only in E2E-Infra nightly runs on self-hosted runners that have the required binaries installed.

---

## Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | reviewer-sea | P1 | FlexAgentClient missing Continue, ListSessions, ResumeSession, CreateSnapshot methods | Incorporated | §2.3 rewritten: added `_stream` helper, `continue_`, `list_sessions`, `resume_session`, `create_snapshot`; `send_message` and `follow_up` moved to streaming |
| 2 | reviewer-sea | P1 | SendMessage uses unary pattern but is server-streaming | Incorporated | §2.3: `send_message`, `continue_`, `follow_up` now use `_stream` returning iterators; `_call` reserved for unary RPCs |
| 3 | reviewer-sea | P2 | Port conflicts between module-scoped fixtures | Incorporated | §12.1/12.2: all fixtures updated to use tier-specific ports from §3.5 (18xxx for Fast, 28xxx for Standard) |
| 4 | reviewer-sea | P2 | Open questions 1-3 blocking implementation | Incorporated | §16 renamed "Decisions" with resolved answers; Q1: manual NDJSON, Q2: Go stubserver, Q3: tier-specific fixed ports; Q4-5: recommended defaults |
| 5 | reviewer-sea | P2 | Stubserver fixture uses private `_start` with incorrect args | Incorporated | §2.4: added `start_stubserver()` to ProcessManager; §12.2: fixture uses `start_stubserver()`; CI builds both binaries |
| 6 | reviewer-sea | P3 | No remote ExecutionEnvironment provider coverage | Incorporated | §8.4 added: optional `test_remote_providers.py` for nightly E2B/Fly testing |
| 7 | reviewer-sea | P3 | cleanup_leaked_sessions calls undefined list_sessions and fixture ordering | Incorporated | §3.3 Layer 3: added fixture ordering note, connection error handling for orchestrator-already-gone case |
