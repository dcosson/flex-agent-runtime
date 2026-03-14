# Code Review: aiag-91v.2 (R1, reviewer-sea)

- Bead: aiag-91v.2
- Commit range: 9513762..89e9d05
- Plan doc: `docs/plans/15-mode2-e2e-test-harness.md`
- Reviewer: reviewer-sea
- Review commit: 89e9d05

## Findings

### P2 - SEC4 input/output boundary tests are no-ops

**Location:** `e2etests/mode2/security_test.go:246-265` (input), `e2etests/mode2/security_test.go:268-285` (output)

**Problem**
`TestSEC4_PTYInputHardening_ControlSequences` constructs 19 malformed input byte slices (control sequences, malformed UTF-8, null bytes, etc.) but never feeds them to the system. Line 263: `_ = input` — each sub-test creates a fresh `DeterministicDriverSimulator` with the standard `buildSimpleReplayScript()` and runs it without any input injection. The test only verifies the simulator doesn't crash on its own, not that the system handles malformed PTY input.

Similarly, `TestSEC4_PTYOutputBoundaryHardening` processes 0 bytes of PTY output (confirmed in test output: "total PTY output processed: 0 bytes"). It uses the same standard replay script with a trivial `OnPTYOutput` callback but the script doesn't produce PTY output.

The plan specifies "Fuzz control sequences and malformed terminal streams to ensure parser robustness." Neither the input nor output test exercises boundary hardening.

**Suggested fix**
For input: use `env.WritePTY(input)` via a `TermmuxEnv` session (similar to F1 tests) to actually feed malformed input to a running PTY session. Verify no panic/crash and that the session remains controllable. For output: build a replay script that includes PTY entries with malformed data (control sequences, overlong UTF-8, etc.) and verify the output callback receives and processes them without panic.

---

### P2 - O2/O3 driver parity oracles use identical replay structure

**Location:** `e2etests/mode2/oracle_test.go:204-233` (`buildDriverReplayScript`), `e2etests/mode2/oracle_test.go:141-163` (O2), `e2etests/mode2/oracle_test.go:170-200` (O3)

**Problem**
Both `TestO2_DriverParityOracle` and `TestO3_NativeReferenceOracle` call `buildDriverReplayScript` which produces structurally identical event sequences — only the `driver` attribute string and `session_id` differ. The lifecycle comparison at lines 152-162 (O2) and 190-199 (O3) will always pass trivially because both sides derive from the same template.

The plan specifies O2 as "Run equivalent scripted scenario on Claude and Codex adapters. Compare normalized lifecycle semantics" — the intent is that different driver types might produce lifecycle events through slightly different mechanisms or timing, and we verify they normalize to the same canonical pattern. With identical inputs, this is a tautology.

Similarly, O3 should compare Mode 2 lifecycle patterns against Mode 1/Native reference patterns. Using the same generator for both defeats the oracle purpose.

**Suggested fix**
Create driver-specific replay scripts that reflect realistic differences between drivers. For O2: the Claude script might have compaction events, multi-turn patterns, and approval flows; the Codex script might have single-turn patterns without compaction. Verify both normalize to the same canonical milestone sequence. For O3: create a Mode 1/Native replay script with different source types (e.g., native events without OTEL) and verify milestone parity.

---

### P2 - P5 attach/detach safety test doesn't perform attach/detach

**Location:** `e2etests/mode2/property_test.go:347-395`

**Problem**
`TestP5_AttachDetachSafety` claims to verify that "Attach/detach operations do not alter driver semantic state beyond connection metadata." However, the "attach/detach cycles" loop at lines 378-387 only calls `mon.State()` repeatedly — it never performs any actual attach or detach operations. The test verifies that calling `State()` N times returns the same value, which is trivially true since nothing changes the monitor between calls.

Compare with `TestS2_LifecycleCommandSimulation_AttachDetachStop` which actually calls `env.Attach()` and `env.Detach()`. P5 should use the property-based framework to generate random attach/detach sequences and verify monitor state stability across those operations.

**Suggested fix**
Within the rapid loop, perform actual attach/detach operations via a `TermmuxEnv` session (or at minimum, simulate client connection/disconnection events through the monitor). Verify that `mon.State()` and `mon.Metrics()` are unchanged after each attach/detach cycle. The current code should be refactored to use something like:
```go
client := env.Attach(fmt.Sprintf("p5-client-%d", i))
env.Detach(fmt.Sprintf("p5-client-%d", i))
```

---

### P3 - B2 benchmark leaks a goroutine per iteration

**Location:** `e2etests/mode2/benchmark_test.go:120-128`

**Problem**
Each benchmark iteration spawns `go func() { for range evtCh {} }()` at line 121 to drain the event channel. However, `unsub()` at line 126 only removes the subscription — it doesn't close `evtCh`. The goroutine blocks forever on the open, unwritten channel. Over many iterations, this accumulates leaked goroutines during the benchmark run.

**Suggested fix**
Close `evtCh` after `unsub()` to allow the drain goroutine to terminate, or restructure to drain synchronously before cleanup. For example:
```go
unsub()
close(evtCh)
// goroutine will now exit via range
mon.Close()
```
Note: verify that `mon.Close()` doesn't also close subscriber channels to avoid double-close.

---

### P3 - ST1 soak has no tier-gating for weekly 12h variant

**Location:** `e2etests/mode2/stress_test.go:23-28`

**Problem**
The plan specifies "12-hour continuous driver sessions" for the Weekly CI tier. The implementation uses `testing.Short()` to skip entirely in short mode, and hardcodes 30 seconds for the non-short variant. There's no environment variable or tier-gating mechanism (like `MODE2_HARNESS_TIER` or `MODE2_ENABLE_SOAK`) to run the full 12h soak in weekly CI. Compare with Mode 3's `MODE3_HARNESS_TIER=nightly` approach.

**Suggested fix**
Add a `MODE2_ENABLE_SOAK` or `MODE2_HARNESS_TIER` environment variable check. Default to 30s for standard CI, but allow the weekly tier to run the full duration. Similar pattern to Mode 3's soak gating that was addressed in aiag-orl.2.

---

## Summary

5 findings: 0 P0, 0 P1, 3 P2, 2 P3

All tests pass clean with `-race` (no data races detected). Benchmarks within targets: B1 p95=237µs (<200ms), B3 p95=59µs (<150ms), B4 p95=45ms (<500ms). Good coverage across P/F/O/S/B/ST/SEC categories. The main issues are three tests that don't actually exercise the behavior they claim to test (SEC4, O2/O3, P5).

**Verdict**: Approved with revisions
