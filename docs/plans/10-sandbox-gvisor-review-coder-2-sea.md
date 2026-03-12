# 10: gVisor Container Management — Review Findings (coder-2-sea, R1)

**Plan:** [10-sandbox-gvisor.md](./10-sandbox-gvisor.md)
**Test Harness:** [10-sandbox-gvisor-test-harness.md](./10-sandbox-gvisor-test-harness.md)
**Reviewer:** coder-2-sea
**Round:** 1
**Date:** 2026-03-11

---

## Summary

The plan and test harness are solid overall, covering the per-tool-call container lifecycle well and including meaningful security, stress, and benchmark testing. However, there are several specification gaps — particularly around rootfs preparation, Close() semantics, and output buffer behavior — that could lead to implementation ambiguity or subtle bugs.

## Findings

### [F1] LimitedBuffer silently drops data and lies to callers — P2

**Section:** Test Harness, P5 (LimitedBuffer)
**Issue:** The test harness asserts that `LimitedBuffer.Write()` returns `len(data), nil` even when the buffer is at capacity and data is being dropped. This violates the standard `io.Writer` contract, which specifies that returning `n < len(p)` indicates a partial write and must accompany a non-nil error. Callers relying on the return value to confirm successful writes will silently lose output data with no indication that truncation occurred.
**Recommendation:** Choose one of three approaches and document the decision: (1) return the actual number of bytes written (i.e., the truncated count) with a nil error, (2) return a sentinel error such as `ErrBufferFull` when truncation occurs, or (3) explicitly document that the `io.Writer` contract is intentionally violated and explain why (e.g., to avoid breaking `io.Copy` pipelines). If option (3), the test should at minimum verify that `LimitedBuffer.Truncated()` or an equivalent flag is set so callers can detect data loss after the fact.

### [F2] No specification of rootfs requirements for containers — P2

**Section:** Plan, container rootfs preparation
**Issue:** The plan references "ZFS bind-mount as container rootfs" and the test harness uses `prepareMinimalRootFS(b)`, but neither document specifies what a valid rootfs must contain. Key questions are unanswered: Does the rootfs need `/bin/sh` or other executables? Are `/dev`, `/proc`, and `/sys` mounted automatically by the OCI runtime spec configuration, or must they be present in the rootfs? What happens if a required path is missing — does container creation fail, or does the executed command fail at runtime?
**Recommendation:** Add a "Rootfs Requirements" subsection to the plan that specifies: (1) the minimal filesystem contents required (e.g., the target binary or `/bin/sh` for shell commands), (2) which virtual filesystems are mounted by the OCI spec and which must be in the rootfs, and (3) which component is responsible for rootfs preparation (sandbox host service, operational setup, or the gvisor manager itself).

### [F3] SEC1 filesystem isolation test passes vacuously — P3

**Section:** Test Harness, SEC1 (Filesystem isolation)
**Issue:** The test reads the host's `/etc/shadow` and then runs `cat /etc/shadow` inside the container, asserting the outputs differ. However, if the container rootfs does not contain `/etc/shadow`, the `cat` command will fail with "No such file or directory" — which also differs from the host's shadow file content, causing the test to pass without actually verifying filesystem isolation. The test proves nothing in this case; it would pass even if the container had full host filesystem access but the file happened to be absent in the rootfs.
**Recommendation:** Replace the test with a more robust approach: write a unique marker file (e.g., `/tmp/gvisor-test-marker-<uuid>`) on the host before container creation, then attempt to read that specific file from inside the container and verify it is not accessible. This proves the container cannot see host filesystem state rather than relying on coincidental file absence.

### [F4] Soak test error rate threshold of 0.1% may be too loose — P2

**Section:** Test Harness, SK2 (Error rate under sustained load)
**Issue:** A 0.1% error rate means 1 in 1,000 tool calls fails. In a realistic 12-hour agent session with ~500 tool calls, this translates to roughly 0.5 failures per session — frequent enough that most long sessions will hit at least one failure. Container failures cause agent retries, wasted tokens, and potentially incorrect behavior if the agent misinterprets a transient failure as a meaningful result.
**Recommendation:** Either tighten the threshold to 0.01% (1 in 10,000), or categorize errors by type and apply different thresholds: transient errors from race conditions or resource pressure could allow a higher rate, while systematic errors (resource leaks, configuration bugs) should have a near-zero threshold. The soak test should also report error categorization in its output so regressions can be diagnosed.

### [F5] No test for OOM kill behavior — P3

**Section:** Test Harness, security tests (SEC series); Plan, cgroup resource limits
**Issue:** The plan specifies cgroup memory limits for containers, and the test harness includes SEC4 for fork bomb defense (PID limit enforcement), but there is no corresponding test for memory exhaustion. OOM kills are a common failure mode in containerized workloads, and the behavior needs to be well-defined: does the container exit with a specific signal? Is the OOM event detectable in `ContainerResult`? Can the manager distinguish an OOM kill from a normal non-zero exit?
**Recommendation:** Add a test (e.g., SEC5) that runs a program which allocates memory exceeding the cgroup limit and verifies: (1) the container is terminated (not hung), (2) the exit status or signal indicates OOM (e.g., SIGKILL with OOM annotation), and (3) `ContainerResult` surfaces the OOM condition in a way that callers can programmatically detect and handle.

### [F6] Close() contract for GVisorManager is underspecified — P2

**Section:** Plan, GVisorManager interface; Test Harness, F5 (concurrent Run + Close)
**Issue:** The test harness F5 tests concurrent `Run()` + `Close()` but the plan does not specify the `Close()` contract. Critical questions: Does `Close()` block until all running containers finish? Does it kill running containers immediately? Is there a grace period or timeout? Does `Run()` called after `Close()` return an error immediately, or does it panic? The absence of this specification means implementations could vary in ways that cause hard-to-debug issues at higher layers.
**Recommendation:** Add explicit `Close()` semantics to the `GVisorManager` interface documentation in the plan. At minimum specify: (1) whether `Close()` is blocking or non-blocking, (2) what happens to in-flight containers (grace period + force kill, or immediate kill), (3) the timeout if applicable, (4) the error returned by `Run()` after `Close()` has been called (e.g., `ErrManagerClosed`), and (5) whether `Close()` is idempotent.

### [F7] Benchmark B4 output capture overhead has no baseline for comparison — P3

**Section:** Test Harness, B4 (Output capture overhead)
**Issue:** The benchmark specifies a target of "< 5% overhead vs direct pipe" for output capture, but the benchmark only measures `Run()` with output capture enabled. There is no baseline measurement of `Run()` without capture (or with a direct pipe) to compute the percentage delta. Without a baseline, the 5% target is unverifiable.
**Recommendation:** Structure B4 as a comparative benchmark: run the same workload with output capture disabled (or piped to `/dev/null`) as the baseline, then with capture enabled as the test case. Report the delta as a percentage. Alternatively, if `Run()` always captures output and there is no "without capture" mode, redefine the target in absolute terms (e.g., "< 2ms overhead per MB of output") and document how it was derived.

---

## Statistics

- Total findings: 7
- P0 (blocking): 0
- P1 (significant): 0
- P2 (moderate): 4
- P3 (minor): 3
