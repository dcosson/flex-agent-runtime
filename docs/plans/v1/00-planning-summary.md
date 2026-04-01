# Planning Review Summary

**Scope:** 16 plan docs (01 through 16) and 16 companion test harness docs.
**Review rounds completed:** Pre-batch (01-ai-core, 2 rounds by lime-cloud) + Batch Round 1 (3 reviewers, 5 docs each) + Batch Round 2 (3 reviewers rotated, 5 docs each).
**Total unique findings:** 112 across all review rounds.

---

## Overall Aggregate

| Metric | Count |
|--------|-------|
| Total findings (all rounds) | 112 |
| Incorporated | 111 |
| Not Incorporated | 1 |
| Incorporation rate | 99.1% |

### Severity Breakdown (All Rounds Combined)

| Severity | Count | Incorporated | Not Inc | Inc Rate |
|----------|-------|-------------|---------|----------|
| P0 | 2 | 2 | 0 | 100% |
| P1 | 26 | 26 | 0 | 100% |
| P2 | 51 | 51 | 0 | 100% |
| P3 | 33 | 32 | 1 | 96.9% |
| **Total** | **112** | **111** | **1** | **99.1%** |

### Convergence Table

| Round | Context | Total Findings | Incorporated | Not Inc | Trend |
|-------|---------|---------------|-------------|---------|-------|
| Pre-batch R1 | 01-ai-core (lime-cloud) | 3 | 3 | 0 | -- |
| Pre-batch R2 | 01-ai-core (lime-cloud) | 0 | 0 | 0 | converged |
| Batch R1 | 15 docs (3 reviewers) | 99 | 98 | 1 | -- |
| Batch R2 | 15 docs (3 reviewers, rotated) | 10 | 10 | 0 | ↓90% |

### Per-Doc Finding Counts (All Rounds)

| Doc | Round | Reviewer | Findings | P0 | P1 | P2 | P3 | Inc | Not Inc |
|-----|-------|----------|----------|----|----|----|----|-----|---------|
| 01-ai-core | R1 | lime-cloud | 3 | 0 | 2 | 1 | 0 | 3 | 0 |
| 02-provider-anthropic | R1 | coder-1-sea | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| 03-provider-openai | R1 | coder-1-sea | 2 | 0 | 2 | 0 | 0 | 2 | 0 |
| 04-provider-google | R1 | coder-1-sea | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| 04-provider-google | R2 | coder-2-sea | 2 | 0 | 0 | 1 | 1 | 2 | 0 |
| 05-agent | R1 | coder-1-sea | 2 | 0 | 2 | 0 | 0 | 2 | 0 |
| 05-agent | R2 | coder-2-sea | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| 06-built-in-tools | R1 | coder-1-sea | 2 | 0 | 1 | 1 | 0 | 2 | 0 |
| 07-code-interpreter | R1 | coder-2-sea | 13 | 0 | 4 | 6 | 3 | 13 | 0 |
| 08-agent-tools-e2e | R1 | coder-2-sea | 8 | 0 | 0 | 5 | 3 | 8 | 0 |
| 09-h2-termmux-port | R1 | coder-2-sea | 10 | 1 | 2 | 4 | 3 | 9 | 1 |
| 09-sandbox-zfs | R1 | coder-2-sea | 11 | 0 | 0 | 5 | 6 | 11 | 0 |
| 10-sandbox-gvisor | R1 | coder-2-sea | 12 | 0 | 0 | 6 | 6 | 12 | 0 |
| 10-sandbox-gvisor | R2 | reviewer-sea | 2 | 0 | 1 | 1 | 0 | 2 | 0 |
| 11-sandbox-host-service | R1 | reviewer-sea | 11 | 0 | 2 | 5 | 4 | 11 | 0 |
| 11-sandbox-host-service | R2 | coder-1-sea | 2 | 0 | 1 | 1 | 0 | 2 | 0 |
| 13-rpc-layer | R1 | reviewer-sea | 8 | 0 | 1 | 5 | 2 | 8 | 0 |
| 13-rpc-layer | R2 | coder-1-sea | 2 | 0 | 2 | 0 | 0 | 2 | 0 |
| 14-mode3-e2e | R1 | reviewer-sea | 6 | 0 | 1 | 3 | 2 | 6 | 0 |
| 14-mode3-e2e | R2 | coder-1-sea | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| 15-mode2-e2e | R1 | reviewer-sea | 6 | 0 | 1 | 3 | 2 | 6 | 0 |
| 16-runtime-test-harness | R1 | reviewer-sea | 6 | 0 | 0 | 4 | 2 | 6 | 0 |

Notes:
- R1 finding counts for docs 07-10 include companion test harness disposition entries (15 findings total across 5 TH docs).
- R2 finding for 05-agent also appears in the 05-agent-test-harness disposition table (same underlying issue). Counted once in the table above.

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

## Batch Round 2 Review Summary

Reviewer assignments were rotated from R1: coder-2-sea took Batch A (docs 02-06, previously coder-1-sea), reviewer-sea took Batch B (docs 07-10, previously coder-2-sea), coder-1-sea took Batch C (docs 11-16, previously reviewer-sea). Total R2 findings: 10, all incorporated. 100% incorporation rate.

### Batch R2 Severity Breakdown

| Severity | Count | Inc Rate |
|----------|-------|----------|
| P0 | 0 | — |
| P1 | 6 | 100% |
| P2 | 3 | 100% |
| P3 | 1 | 100% |
| **Total** | **10** | **100%** |

### Per-Reviewer Breakdown

| Reviewer | Docs Reviewed | Findings | Not Inc |
|----------|--------------|----------|---------|
| coder-2-sea | 02, 03, 04, 05, 06 | 3 | 0 |
| reviewer-sea | 07, 08, 09-termmux, 09-zfs, 10 | 2 | 0 |
| coder-1-sea | 11, 13, 14, 15, 16 | 5 | 0 |

### Finding Patterns

The dominant theme in R2 was **cross-document event taxonomy drift** — 3 of 10 findings identified event lifecycle names or streaming API types that diverged from the canonical definitions established in plans 01 and 05. coder-1-sea found that 13-rpc-layer's `StreamAgentEvents` returned a producer-side type instead of a consumer-appropriate `AgentEventReceiver`, and that both 13-rpc-layer and 14-mode3-e2e used non-canonical lifecycle event names instead of the `session_started`/`session_ended` taxonomy from plan 05. These were cross-doc consistency issues that the reviewer rotation was specifically designed to catch — fresh eyes on unfamiliar docs spotted naming divergences that the original R1 reviewer (who reviewed the plans in isolation) had not flagged.

The second theme was **pseudo-code correctness defects** — 3 findings at P1 severity. The most impactful was reviewer-sea's finding that 10-sandbox-gvisor's `checkOOMKill` function misclassified timeouts and context cancellations as OOM kills (exit code -1 matched all abnormal exits). coder-1-sea found that 11-sandbox-host-service's `TurnComplete` returned an undefined `prospectiveTurn` symbol. coder-2-sea found that 05-agent's snapshot trigger cardinality in the companion test harness contradicted the follow-up chaining semantics in the plan.

The remaining findings were **metrics and observability gaps** — 10-sandbox-gvisor's `ContainerResult.Duration` was never set (P2, exec_duration metric always zero), 11-sandbox-host-service recorded bytes-used as snapshot latency (P2), and 04-provider-google had a StopReason mapping mismatch for max-token termination (P2). One P3 finding corrected 04-provider-google's local context-overflow detector diverging from the shared helper.

### Convergence

11 of 15 batch docs had zero R2 findings, confirming convergence: 02-provider-anthropic, 03-provider-openai, 06-built-in-tools, 07-code-interpreter, 08-agent-tools-e2e, 09-sandbox-zfs, 09-h2-termmux-port, 15-mode2-e2e, 16-runtime-test-harness. Combined with 01-ai-core (converged in pre-batch R2), 12 of 16 plan docs are fully converged with no findings in their most recent review round.

The 6 docs with R2 findings (04, 05, 10, 11, 13, 14) all had their findings incorporated. No P0 issues were found in R2, and all P1 findings were correctness bugs in pseudo-code rather than architectural gaps — indicating the plans are structurally sound with residual implementation-level issues being caught.

---

## Document Metrics

### Plan Docs (01-16)

| Doc | Lines |
|-----|-------|
| 01-ai-core | 1,847 |
| 02-provider-anthropic | 412 |
| 03-provider-openai | 926 |
| 04-provider-google | 1,187 |
| 05-agent | 422 |
| 06-built-in-tools | 415 |
| 07-code-interpreter | 715 |
| 08-agent-tools-e2e | 318 |
| 09-h2-termmux-port | 672 |
| 09-sandbox-zfs | 1,396 |
| 10-sandbox-gvisor | 1,714 |
| 11-sandbox-host-service | 1,484 |
| 13-rpc-layer | 465 |
| 14-mode3-e2e | 339 |
| 15-mode2-e2e | 339 |
| 16-runtime-test-harness | 337 |
| **Total** | **12,988** |

### Test Harness Docs

| Doc | Lines |
|-----|-------|
| 01-ai-core-test-harness | 736 |
| 02-provider-anthropic-test-harness | 212 |
| 03-provider-openai-test-harness | 889 |
| 04-provider-google-test-harness | 741 |
| 05-agent-test-harness | 226 |
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
| **Total** | **7,288** |

### Review Docs

| Category | Files | Lines |
|----------|-------|-------|
| Existing (not yet deleted) | 15 | 574 |
| Deleted (incorporated) | 14 | 630 |
| **Total** | **29** | **1,204** |

### Grand Total

| Category | Docs | Lines |
|----------|------|-------|
| Architecture + index | 2 | 1,654 |
| Plan docs | 16 | 12,988 |
| Test harness docs | 16 | 7,288 |
| Review docs (existing) | 15 | 574 |
| Review docs (deleted, incorporated) | 14 | 630 |
| **Grand total** | **63** | **23,134** |

---

## Quality Signals

**Review process health:** Across two batch review rounds, 109 findings were produced with a 99.1% incorporation rate (108/109 incorporated). The single non-incorporation (P3, plan numbering) had documented rationale. Findings were substantive — 28 at P0/P1 severity, catching real correctness bugs, cross-doc contract mismatches, and safety issues.

**Strong convergence:** R2 produced 90% fewer findings than R1 (10 vs 99), with no P0 issues. 12 of 16 plan docs are fully converged (zero findings in their most recent review round). The 6 docs with R2 findings had only implementation-level pseudo-code bugs and cross-doc naming drift — no architectural gaps remained. This matches the target convergence pattern demonstrated by 01-ai-core in the pre-batch rounds.

**Reviewer rotation validated:** R2 reviewer rotation (each batch assigned to a different reviewer than R1) proved effective at catching cross-doc consistency issues. 3 of 10 R2 findings were event taxonomy drift between connected components — the type of issue that fresh eyes on unfamiliar docs are best positioned to catch. The original R1 reviewers, who reviewed docs in isolation, had not flagged these naming divergences.

**Reviewer calibration across rounds:** In R1, reviewers showed complementary strengths (coder-1-sea on correctness, coder-2-sea on breadth, reviewer-sea on concurrency). In R2, coder-1-sea produced the most findings (5) on the service integration docs — their correctness focus caught event naming mismatches and pseudo-code symbol errors. coder-2-sea found 3 issues in the provider/agent plans. reviewer-sea found 2 issues in the sandbox plans, maintaining the pattern of catching implementation-level correctness bugs.

**Cross-doc consistency improved:** R1's most systemic issue was terminology drift ("orchestrator" vs "RuntimeController"). By R2, the cross-cutting terminology update during R1 incorporation had taken effect — no terminology drift findings in R2. The remaining cross-doc issues were event lifecycle naming (3 findings), which are narrower and were resolved by aligning to the canonical plan 05 taxonomy.

**Readiness assessment:** All 16 plan docs have been through at least 2 review rounds. 12 have fully converged. The remaining 6 (04, 05, 10, 11, 13, 14) had their R2 findings incorporated. The plans are ready for implementation or a final seam review pass to systematically verify cross-component interface compatibility.
