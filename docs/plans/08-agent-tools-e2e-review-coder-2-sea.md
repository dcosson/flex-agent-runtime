# 08: Agent + Tools E2E — Review Findings (coder-2-sea, R1)

**Plan:** [08-agent-tools-e2e.md](./08-agent-tools-e2e.md)
**Test Harness:** [08-agent-tools-e2e-test-harness.md](./08-agent-tools-e2e-test-harness.md)
**Reviewer:** coder-2-sea
**Round:** 1
**Date:** 2026-03-11

---

## Summary

The plan provides solid coverage of the integrated agent loop + tools + code interpreter stack, with clear scenario definitions and a well-structured test harness. However, there are gaps around error recovery testing, git tool coverage, and code interpreter RLM sub-call semantics that should be addressed before implementation.

## Findings

### [F1] Code interpreter E2E scenario underspecifies RLM sub-call handling — P2

**Section:** 4.5 (Code Interpreter scenario)
**Issue:** Section 4.5 references "RLM sub-calls and DataStore operations" but does not specify how the fake provider should handle nested LLM calls originating from within the code interpreter, as distinct from the main agent loop's provider calls. If the code interpreter issues its own LLM sub-calls (e.g., for tool-use decisions within a sandboxed execution), `provider_fake.go` needs to support distinguishing and routing these nested calls separately. This is a non-trivial testing challenge — the fake provider must maintain call-stack context or use some form of call tagging to differentiate top-level agent loop calls from code interpreter sub-calls.
**Recommendation:** Add a subsection to the code interpreter scenario specifying: (a) whether the fake provider is expected to handle nested/sub-calls, (b) if so, the mechanism for distinguishing them (e.g., a call-context header or request metadata), and (c) at least one scenario assertion that validates correct RLM sub-call routing. If nested calls are not in scope for E2E, state that explicitly and note it as a limitation.

### [F2] No git tool E2E scenario — P2

**Section:** 4.1-4.6 (Scenario matrix)
**Issue:** The scenario matrix covers read/write/edit, bash, and grep/glob (implicitly), but there is no explicit scenario exercising git operations. Git ops are a core built-in tool defined in plan 06 and represent a significant surface area — read operations (log, diff, status), branch operations, and commit workflows all pass through distinct code paths. Omitting git from E2E means a critical tool category is only validated at the unit/integration level.
**Recommendation:** Add a scenario (e.g., 4.7) that exercises at least git read operations (status, log, diff) within a test workspace that has been initialized as a git repository with known commit history. Verify that the agent can invoke git tools and that outputs are correctly captured in the conversation trace.

### [F3] Mermaid diagram node labels use invalid string concatenation — P3

**Section:** 2.1 (Architecture diagram)
**Issue:** The Mermaid diagram uses `\n` within node labels (e.g., `TOOLS[internal/tools]\n CODEINTERP[internal/tools/codeinterp]`), which does not render correctly in most Mermaid renderers. This produces a malformed diagram rather than the intended multi-node layout.
**Recommendation:** Split into two separate node definitions on separate lines, e.g., `TOOLS[internal/tools]` and `CODEINTERP[internal/tools/codeinterp]` as distinct nodes with their own edges.

### [F4] Scenario matrix lacks error recovery testing — P2

**Section:** 4.1-4.6 (Scenario matrix)
**Issue:** All defined scenarios cover happy-path or controlled-failure paths (e.g., intentional tool errors with expected error messages). There is no scenario testing the agent's behavior when a tool call fails unexpectedly mid-workflow and the agent must recover — for example, a file-not-found error during an edit step, a permission-denied error during bash execution, or a transient failure that the agent should retry or work around. Error recovery is a critical real-world behavior pattern, and E2E is the appropriate level to validate it.
**Recommendation:** Add at least one scenario (e.g., 4.8) where the fake provider's scripted conversation includes a tool call that returns an error, followed by expected agent recovery behavior (retry with corrected parameters, fallback to alternative approach, or graceful error reporting). Define explicit assertions on the recovery path taken.

### [F5] Exit criteria omit RLM budget enforcement at E2E level — P3

**Section:** Exit criteria
**Issue:** Exit criterion 5 mentions "script trace shows progressive discovery, RLM usage stats" but does not explicitly require validation of RLM budget enforcement. Since budget enforcement is a critical safety feature — preventing runaway code interpreter sessions from consuming unbounded LLM resources — it should be verified at the E2E level, not just at the unit/integration level.
**Recommendation:** Add an exit criterion (or extend criterion 5) requiring at least one E2E scenario that demonstrates RLM budget limits are enforced: a code interpreter session that would exceed its budget is terminated with an appropriate error, and the agent handles the budget-exceeded signal correctly.

### [F6] Property test uses outdated "Scripting" terminology — P2

**Section:** Test harness, P4 property test
**Issue:** The P4 property test is titled "Scripting Trace Consistency" and refers to "scripted flows" throughout. This uses the old "scripting" terminology that has been renamed to "code interpreter" across the rest of the plans. Inconsistent naming creates confusion about whether this refers to the code interpreter subsystem or to the test harness's own scripted fake-provider flows.
**Recommendation:** Rename to "Code Interpreter Trace Consistency" and update all references from "scripting" / "scripted flows" to "code interpreter" where referring to the subsystem. Reserve "scripted" only for the test harness concept of scripted provider responses.

### [F7] No specification of workspace cleanup or retention strategy — P3

**Section:** General (optimization section mentions "isolated temp workspaces")
**Issue:** The optimization section mentions "isolated temp workspaces" but the main design does not specify the cleanup strategy. Key questions left unanswered: Are workspaces preserved on test failure for debugging? Is there a configurable retention policy (e.g., keep last N failures)? Are workspaces cleaned up in CI but preserved locally? Without clarity here, implementations may diverge or debugging failed E2E tests in CI may be unnecessarily difficult.
**Recommendation:** Add a subsection to the test harness design specifying: (a) workspaces are cleaned up on success by default, (b) workspaces are preserved on failure with their path printed to test output, (c) an environment variable (e.g., `E2E_KEEP_WORKSPACES=true`) allows overriding cleanup for local debugging.

---

## Statistics

- Total findings: 7
- P0 (blocking): 0
- P1 (significant): 0
- P2 (moderate): 4
- P3 (minor): 3
