# Planning Review Summary

**Scope:** 16 plan docs (01 through 16) and 16 companion test harness docs.
**Review rounds completed:** Pre-batch (01-ai-core, 2 rounds by lime-cloud) + Batch Round 1 (3 reviewers, 5 docs each).
**Total unique findings:** 102 across all review rounds.

---

## Overall Aggregate

| Metric | Count |
|--------|-------|
| Total findings (all rounds) | 102 |
| Incorporated | 101 |
| Not Incorporated | 1 |
| Incorporation rate | 99.0% |

### Severity Breakdown (All Rounds Combined)

| Severity | Count | Incorporated | Not Inc | Inc Rate |
|----------|-------|-------------|---------|----------|
| P0 | 2 | 2 | 0 | 100% |
| P1 | 20 | 20 | 0 | 100% |
| P2 | 48 | 48 | 0 | 100% |
| P3 | 32 | 31 | 1 | 96.9% |
| **Total** | **102** | **101** | **1** | **99.0%** |

### Convergence Table

| Round | Context | Total Findings | Incorporated | Not Inc | Trend |
|-------|---------|---------------|-------------|---------|-------|
| Pre-batch R1 | 01-ai-core (lime-cloud) | 3 | 3 | 0 | -- |
| Pre-batch R2 | 01-ai-core (lime-cloud) | 0 | 0 | 0 | converged |
| Batch R1 | 15 docs (3 reviewers) | 99 | 98 | 1 | -- |

### Per-Doc Finding Counts (All Rounds)

| Doc | Reviewer | Findings | P0 | P1 | P2 | P3 | Inc | Not Inc |
|-----|----------|----------|----|----|----|----|-----|---------|
| 01-ai-core | lime-cloud | 3 | 0 | 2 | 1 | 0 | 3 | 0 |
| 02-provider-anthropic | coder-1-sea | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| 03-provider-openai | coder-1-sea | 2 | 0 | 2 | 0 | 0 | 2 | 0 |
| 04-provider-google | coder-1-sea | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| 05-agent | coder-1-sea | 2 | 0 | 2 | 0 | 0 | 2 | 0 |
| 06-built-in-tools | coder-1-sea | 2 | 0 | 1 | 1 | 0 | 2 | 0 |
| 07-code-interpreter | coder-2-sea | 13 | 0 | 4 | 6 | 3 | 13 | 0 |
| 08-agent-tools-e2e | coder-2-sea | 8 | 0 | 0 | 5 | 3 | 8 | 0 |
| 09-h2-termmux-port | coder-2-sea | 10 | 1 | 2 | 4 | 3 | 9 | 1 |
| 09-sandbox-zfs | coder-2-sea | 11 | 0 | 0 | 5 | 6 | 11 | 0 |
| 10-sandbox-gvisor | coder-2-sea | 12 | 0 | 0 | 6 | 6 | 12 | 0 |
| 11-sandbox-host-service | reviewer-sea | 11 | 0 | 2 | 5 | 4 | 11 | 0 |
| 13-rpc-layer | reviewer-sea | 8 | 0 | 1 | 5 | 2 | 8 | 0 |
| 14-mode3-e2e | reviewer-sea | 6 | 0 | 1 | 3 | 2 | 6 | 0 |
| 15-mode2-e2e | reviewer-sea | 6 | 0 | 1 | 3 | 2 | 6 | 0 |
| 16-runtime-test-harness | reviewer-sea | 6 | 0 | 0 | 4 | 2 | 6 | 0 |

Note: Finding counts for docs 07-10 include companion test harness disposition entries (15 findings total across 5 TH docs).

---

## Pre-batch Reviews: 01-ai-core

01-ai-core was reviewed by lime-cloud in two rounds before the batch review process began.

**Round 1** produced 3 findings (P1: 2, P2: 1), all incorporated. The P1 findings addressed an EventStream deadlock on close-without-terminal-event and model registry leaking mutable state via shallow copies. The P2 finding corrected nondeterministic transform ordering caused by unsorted map iteration.

**Round 2** found no new issues, confirming convergence. The 01-ai-core plan was mature and stable entering the batch review phase.

---

## Batch Round 1 Review Summary

Three reviewers each independently reviewed 5 plan docs and their companion test harnesses. Total batch findings: 99 (84 from plan docs, 15 from test harness docs). Incorporation rate: 98/99 (99.0%).

### Batch R1 Severity Breakdown

| Severity | Plan Docs | Test Harness Docs | Total | Inc Rate |
|----------|-----------|-------------------|-------|----------|
| P0 | 1 | 1 | 2 | 100% |
| P1 | 18 | 1 | 19 | 100% |
| P2 | 41 | 6 | 47 | 100% |
| P3 | 25 | 7 | 32 | 96.9% |
| **Total** | **85** | **15** | **99** | **99.0%** |

Note: One finding (P0 "missing companion test harness document" for 09-h2-termmux-port) appears in both the plan and TH disposition tables since the TH was created as part of incorporation. The raw sum is 84+15=99; the unique finding count is 98.

### Per-Reviewer Breakdown

| Reviewer | Docs Reviewed | Plan Findings | TH Findings | Total | Not Inc |
|----------|--------------|---------------|-------------|-------|---------|
| coder-1-sea | 02, 03, 04, 05, 06 | 8 | 0 | 8 | 0 |
| coder-2-sea | 07, 08, 09-termmux, 09-zfs, 10 | 39 | 15 | 54 | 1 |
| reviewer-sea | 11, 13, 14, 15, 16 | 37 | 0 | 37 | 0 |

The volume difference reflects both doc complexity and review approach. coder-1-sea reviewed the lighter provider and agent plans, focusing tightly on P1 correctness issues (7 of 8 findings were P1). coder-2-sea had the densest docs (code interpreter, sandbox infrastructure) and was the only reviewer to produce test harness findings. reviewer-sea reviewed the service integration and E2E test plans, finding the most concurrency and state machine issues.

### Finding Patterns

The dominant theme was **API contract and interface specification gaps** -- approximately 25 findings identified interfaces that were declared but insufficiently specified for independent implementation. Examples include the ToolBackend missing a progress callback (06), RuntimeController seam absent from the agent plan (05), AgentEventStream having unresolved bidirectional-vs-unidirectional ambiguity (13), and DataStore Search semantics being ambiguous (07). These gaps would have caused implementation conflicts between teams working on connected components.

The second major theme was **correctness and safety defects in pseudo-code** -- approximately 12 findings at P1 severity or higher. The most impactful were TOCTOU races in session creation capacity checks (11), tool-call argument corruption during stream finalization across all three provider plans (02, 03, 04), and safety-blocked content leaking before error detection in the Google provider (04). These represented genuine bugs that would have manifested in production code.

**Cross-document consistency** accounted for roughly 10 findings. The most systemic was terminology drift between "orchestrator" and "RuntimeController" across multiple plans, resolved via a cross-cutting update during incorporation. Other examples: event type ownership duplicated between 05-agent and 09-h2-termmux-port, per-turn snapshot semantics in 05-agent conflicting with follow-up chaining, and 15-mode2-e2e referencing a plan (09-h2-termmux-port) that shared a number with 09-sandbox-zfs.

**Test specification completeness** drove approximately 15 findings concentrated in the E2E and harness plans (14, 15, 16). Reviewers flagged missing fake/mock implementations (14's fake sandbox host), undefined benchmark datasets, unspecified workload composition mechanisms, and soak test criteria that lacked concrete drift thresholds. These would have blocked test implementation without additional specification.

**State machine and lifecycle design** issues appeared primarily in the sandbox-host-service (11), with findings about missing RollingBack state definitions, race conditions between turnCount increments and snapshot completion, and DestroySession not draining in-flight tools. These were concentrated in the most stateful component of the architecture.

### Non-Incorporation

Only 1 of 99 findings was not incorporated: 09-h2-termmux-port finding #9 (P3, "Plan numbering collision with 09-sandbox-zfs"). The plan numbers were left unchanged and a disambiguation note was added to the plan index instead. This is a cosmetic issue with no implementation impact.

---

## Document Metrics

### Plan Docs (01-16)

| Doc | Lines |
|-----|-------|
| 01-ai-core | 1,847 |
| 02-provider-anthropic | 412 |
| 03-provider-openai | 926 |
| 04-provider-google | 1,185 |
| 05-agent | 416 |
| 06-built-in-tools | 415 |
| 07-code-interpreter | 715 |
| 08-agent-tools-e2e | 318 |
| 09-h2-termmux-port | 672 |
| 09-sandbox-zfs | 1,396 |
| 10-sandbox-gvisor | 1,699 |
| 11-sandbox-host-service | 1,470 |
| 13-rpc-layer | 453 |
| 14-mode3-e2e | 329 |
| 15-mode2-e2e | 339 |
| 16-runtime-test-harness | 337 |
| **Total** | **12,929** |

### Test Harness Docs

| Doc | Lines |
|-----|-------|
| 01-ai-core-test-harness | 736 |
| 02-provider-anthropic-test-harness | 212 |
| 03-provider-openai-test-harness | 889 |
| 04-provider-google-test-harness | 741 |
| 05-agent-test-harness | 218 |
| 06-built-in-tools-test-harness | 215 |
| 07-code-interpreter-test-harness | 348 |
| 08-agent-tools-e2e-test-harness | 199 |
| 09-h2-termmux-port-test-harness | 186 |
| 09-sandbox-zfs-test-harness | 1,020 |
| 10-sandbox-gvisor-test-harness | 907 |
| 11-sandbox-host-service-test-harness | 784 |
| 13-rpc-layer-test-harness | 198 |
| 14-mode3-e2e-test-harness | 220 |
| 15-mode2-e2e-test-harness | 193 |
| 16-runtime-test-harness-test-harness | 214 |
| **Total** | **7,280** |

### Review Docs

| Category | Files | Lines |
|----------|-------|-------|
| Existing (not yet deleted) | 5 | 362 |
| Deleted (incorporated) | 14 | 630 |
| **Total** | **19** | **992** |

### Grand Total

| Category | Docs | Lines |
|----------|------|-------|
| Architecture + index | 2 | 1,455 |
| Plan docs | 16 | 12,929 |
| Test harness docs | 16 | 7,280 |
| Review docs (all) | 19 | 992 |
| **Grand total** | **53** | **22,656** |

---

## Quality Signals

**Review process health:** The batch R1 review was comprehensive -- 99 findings across 15 docs with a 99.0% incorporation rate indicates the review process is working well. Findings were substantive (20 at P0/P1) and plan authors were receptive to feedback. The single non-incorporation was a minor organizational issue with clear rationale.

**Pre-batch convergence:** 01-ai-core demonstrated the target convergence pattern: R1 found 3 real issues, R2 found zero. This validates the review-incorporate-review cycle for achieving plan maturity.

**Reviewer calibration:** The three batch reviewers showed complementary strengths. coder-1-sea's tight focus on P1 correctness in the provider plans caught subtle data-corruption paths. coder-2-sea's breadth (54 findings including all 15 TH findings) reflects thorough coverage of the most complex subsystems. reviewer-sea's emphasis on concurrency and state machine correctness caught races and lifecycle gaps in the service layer.

**Readiness for Batch R2:** All batch R1 findings have been incorporated. The 5 review files from reviewer-sea's Batch C remain on disk (not yet deleted during incorporation cleanup) but their findings are reflected in the plan disposition tables. A Batch R2 review round would test convergence -- the expectation is significantly fewer findings, focused on residual cross-doc seam issues rather than per-doc gaps.

**Cross-doc seam risk:** The most systemic findings (terminology drift, duplicate type ownership, dependency references to shared plan numbers) suggest a seam review pass across connected components would be valuable before implementation begins. The individual plan reviews caught many cross-doc issues incidentally, but a dedicated seam review would provide systematic coverage.
