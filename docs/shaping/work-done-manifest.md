---
shaping: true
---

# Work-Done Manifest — Shaping

## Source

> A generic, workflow-agnostic way to record proof that work was completed.
> Must be git-native (structured files tracked in version control) — this is the only source of truth guaranteed available in any codebase.
> Should NOT be coupled to beads, plan-* skills, or any specific team workflow structure.
> Teams build their own workflows on top (our plan-review cycles, other teams' different processes).
> The underlying format just captures: what was done, evidence (commit hashes, test results), verification steps passed, who attested.
>
> Examples of what it could track:
> - Planning: proof that plan was written, reviewed in multiple rounds, seam reviewed, marked complete
> - Implementation: code implemented, reviewed, plan signoff passed, follow-ups completed
> - Any other workflow a team defines

---

## Problem

Today, proof of work completion is scattered across multiple systems and formats: bead status fields, plan doc signoff sections, review disposition tables, git commit messages, CI logs. There's no unified, queryable record that says "this piece of work went through steps X, Y, Z and all passed." This makes it hard to:

1. **Audit what happened** — Reconstructing the full lifecycle of a piece of work requires reading multiple files, bead JSON, git logs, and h2 message history.
2. **Enforce workflows** — There's no machine-readable way to say "a plan must be reviewed before implementation beads are created" and verify it was followed.
3. **Port workflows** — Our plan-review-implement-signoff cycle is baked into specific skills. Other teams can't adopt parts of it without adopting all of it.
4. **Measure process health** — No structured data to answer "what percentage of plans had 2+ review rounds before implementation?"

## Outcome

A simple, git-native manifest format where:
- Any workflow step can record structured evidence that it was completed
- The format is generic enough for any team's process, not just ours
- Manifests can be validated, queried, and composed by tooling
- The evidence chain is auditable: who attested, when, with what proof

---

## Requirements (R)

| ID | Requirement | Status |
|----|-------------|--------|
| R0 | Record structured proof that a unit of work completed a workflow step, with evidence | Core goal |
| R1 | Git-native: manifests are files tracked in version control, no external DB required | Must-have |
| R2 | Workflow-agnostic: the format itself makes no assumptions about what workflows exist — workflows are defined by teams, not by the manifest schema | Must-have |
| R3 | Evidence linking: each attestation can reference concrete artifacts (commit SHAs, file paths, test command + result, CI run IDs) | Must-have |
| R4 | Attestation identity: record who (agent name, human, CI system) attested to the completion | Must-have |
| R5 | Composable: a manifest can reference other manifests, enabling hierarchical workflows (e.g., "implementation complete" requires "all sub-task manifests complete") | Must-have |
| R6 | Machine-readable: tooling can parse manifests to validate workflow compliance, generate reports, and block downstream steps | Must-have |
| R7 | Human-scannable: a developer should be able to open a manifest file and understand what happened without special tooling | Nice-to-have |
| R8 | Incremental: steps can be added to a manifest over time as work progresses, not only written once at the end | Must-have |

---

## A: Single-File YAML Manifest

Each unit of work gets one YAML file. The file accumulates attestation records as workflow steps complete. Workflow definitions are separate YAML files that declare what steps exist and their ordering constraints.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **A1** | **Manifest file**: One YAML file per work unit (e.g., `.manifests/plan-01-ai-core.yaml`). Contains metadata (subject, workflow ref) and an ordered list of attestation entries. | |
| **A2** | **Attestation entry**: Each entry records: step name, timestamp, attester identity, status (pass/fail/skip), and an evidence block (list of `{type, ref}` pairs — commit SHA, file path, command output hash, etc.) | |
| **A3** | **Workflow definition file**: Separate YAML files (e.g., `.manifests/workflows/plan-review-cycle.yaml`) declare step names, ordering constraints (sequential, parallel groups), and required-vs-optional steps. Manifests reference a workflow by name. | |
| **A4** | **Validation CLI**: A small CLI tool reads a manifest + its workflow definition and reports: which steps are complete, which are pending, whether ordering constraints are satisfied. Exit code 0/1 for CI gating. | |
| **A5** | **Manifest references**: An attestation's evidence block can include `{type: manifest, ref: path/to/other.yaml}` to compose hierarchical workflows. Validation recurses into referenced manifests. | |

### Example manifest file (A)

```yaml
subject: plan/01-ai-core
workflow: plan-review-cycle
created: 2026-03-01T10:00:00Z

attestations:
  - step: plan-drafted
    timestamp: 2026-03-01T10:30:00Z
    attester: coder-1-sea
    status: pass
    evidence:
      - type: commit
        ref: abc1234
      - type: file
        ref: docs/plans/01-ai-core.md

  - step: review-round-1
    timestamp: 2026-03-02T14:00:00Z
    attester: reviewer-sea
    status: pass
    evidence:
      - type: commit
        ref: def5678
      - type: file
        ref: docs/plans/01-ai-core-review-reviewer-sea.md

  - step: review-incorporated-1
    timestamp: 2026-03-03T09:00:00Z
    attester: coder-1-sea
    status: pass
    evidence:
      - type: commit
        ref: 789abcd
      - type: command
        ref: "935/992 findings incorporated"
```

### Example workflow definition (A)

```yaml
name: plan-review-cycle
description: Plan doc lifecycle from draft through review convergence

steps:
  - name: plan-drafted
    required: true

  - name: review-round-1
    required: true
    after: [plan-drafted]

  - name: review-incorporated-1
    required: true
    after: [review-round-1]

  - name: review-round-2
    required: false
    after: [review-incorporated-1]

  - name: seam-review
    required: true
    after: [review-incorporated-1]

  - name: plan-complete
    required: true
    after: [seam-review]
```

---

## B: Markdown Manifest with Frontmatter Schema

Each unit of work gets a Markdown file with YAML frontmatter for machine-readable data and a human-readable body. Workflow definitions are embedded as frontmatter schemas or separate Markdown files.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **B1** | **Manifest file**: One Markdown file per work unit (e.g., `.manifests/plan-01-ai-core.md`). YAML frontmatter contains subject, workflow ref, and attestation records. Body contains human-readable narrative. | |
| **B2** | **Attestation entries in frontmatter**: Same structure as A2 but in YAML frontmatter. The Markdown body can include prose descriptions, links, and context that wouldn't fit in structured data. | |
| **B3** | **Workflow definition**: A Markdown file (e.g., `.manifests/workflows/plan-review-cycle.md`) with workflow steps in frontmatter and prose description in body. | |
| **B4** | **Validation CLI**: Same as A4 — parses frontmatter from Markdown files. | |
| **B5** | **Manifest references**: Same as A5 — evidence entries can point to other manifest files. | |

### Example manifest file (B)

```markdown
---
subject: plan/01-ai-core
workflow: plan-review-cycle
created: 2026-03-01T10:00:00Z
attestations:
  - step: plan-drafted
    timestamp: 2026-03-01T10:30:00Z
    attester: coder-1-sea
    status: pass
    evidence:
      - {type: commit, ref: abc1234}
      - {type: file, ref: docs/plans/01-ai-core.md}
  - step: review-round-1
    timestamp: 2026-03-02T14:00:00Z
    attester: reviewer-sea
    status: pass
    evidence:
      - {type: commit, ref: def5678}
---

# Plan 01-ai-core — Work Manifest

## Plan Drafted
Coder-1-sea drafted the plan doc covering core AI message types,
provider registry, and event stream architecture.

## Review Round 1
Reviewer-sea reviewed and found 992 findings (23 P0, 45 P1).
935 incorporated in round 1 incorporation pass.
```

---

## C: JSON Lines Append-Only Log

Each unit of work gets a `.jsonl` file where each line is an attestation event. Workflow definitions are JSON Schema files. The append-only format makes incremental writing trivial (just append a line) and is natural for streaming/CI integration.

| Part | Mechanism | Flag |
|------|-----------|:----:|
| **C1** | **Manifest file**: One `.jsonl` file per work unit (e.g., `.manifests/plan-01-ai-core.jsonl`). Each line is a self-contained JSON attestation event. First line is a header event with subject and workflow ref. | |
| **C2** | **Attestation event**: JSON object with fields: `step`, `timestamp`, `attester`, `status`, `evidence[]`. Each event is independently parseable. | |
| **C3** | **Workflow definition**: JSON Schema file (e.g., `.manifests/workflows/plan-review-cycle.schema.json`) declaring valid step names, ordering constraints, and required/optional. | |
| **C4** | **Validation CLI**: Reads the `.jsonl` file, reconstructs state, validates against the workflow schema. | |
| **C5** | **Manifest references**: Evidence entries can reference other `.jsonl` manifest files. | |

### Example manifest file (C)

```jsonl
{"type":"header","subject":"plan/01-ai-core","workflow":"plan-review-cycle","created":"2026-03-01T10:00:00Z"}
{"step":"plan-drafted","timestamp":"2026-03-01T10:30:00Z","attester":"coder-1-sea","status":"pass","evidence":[{"type":"commit","ref":"abc1234"}]}
{"step":"review-round-1","timestamp":"2026-03-02T14:00:00Z","attester":"reviewer-sea","status":"pass","evidence":[{"type":"commit","ref":"def5678"}]}
```

---

## Fit Check

| Req | Requirement | Status | A | B | C |
|-----|-------------|--------|---|---|---|
| R0 | Record structured proof with evidence | Core goal | ✅ | ✅ | ✅ |
| R1 | Git-native: files in version control | Must-have | ✅ | ✅ | ✅ |
| R2 | Workflow-agnostic: no baked-in workflow assumptions | Must-have | ✅ | ✅ | ✅ |
| R3 | Evidence linking: commit SHAs, file paths, test results | Must-have | ✅ | ✅ | ✅ |
| R4 | Attestation identity: who attested | Must-have | ✅ | ✅ | ✅ |
| R5 | Composable: manifests can reference other manifests | Must-have | ✅ | ✅ | ✅ |
| R6 | Machine-readable: tooling can parse and validate | Must-have | ✅ | ✅ | ✅ |
| R7 | Human-scannable: readable without tooling | Nice-to-have | ✅ | ✅ | ❌ |
| R8 | Incremental: steps added over time | Must-have | ✅ | ✅ | ✅ |

**Notes:**

- C fails R7: JSONL is not human-scannable — each line is a dense JSON blob. Reading the full lifecycle requires tooling or careful manual parsing.
- A and B both pass all checks. The key difference is trade-off between pure data (A: YAML only) vs. mixed data+narrative (B: frontmatter + prose).

---

## Shape Analysis

**A vs B — the real question**: Both shapes share identical data semantics (attestation entries, workflow definitions, evidence linking, composition). The difference is purely in file format:

- **Shape A** (pure YAML) is cleaner for machine consumption. One format, one parser. No ambiguity about where data lives. But attestation entries can feel dense when you want to understand *why* something happened, not just *that* it happened.

- **Shape B** (Markdown + frontmatter) lets attestation data live alongside narrative context. This is appealing because manifests serve two audiences: tooling (needs structured data) and people reviewing the audit trail (want context). However, it introduces format complexity — the frontmatter can grow very large, making the file awkward to edit.

- **Shape C** (JSONL) has the best append semantics (just add a line) but worst readability. It's also the most git-unfriendly — JSONL diffs are hard to review in PRs.

**Recommendation**: Shape A is the strongest starting point. Pure YAML keeps the format simple and tool-friendly while remaining human-readable. If narrative context is needed, the evidence block can reference files that contain the narrative (review docs, plan docs). The manifest itself stays focused on structured proof.

---

## Open Questions

1. **Where do manifest files live?** Options: `.manifests/` at repo root, `docs/manifests/`, or co-located next to the work artifacts (e.g., `docs/plans/01-ai-core.manifest.yaml` next to `docs/plans/01-ai-core.md`). Co-location has discoverability benefits but could clutter directories.

2. **Workflow definition ownership**: Should workflow definitions live in the repo (per-project) or in a shared config directory (per-team)? Per-repo is more portable; per-team allows standardization.

3. **Attestation granularity**: Should a single review round be one attestation, or should each reviewer's review be a separate attestation? Finer granularity enables better querying but increases manifest size.

4. **Immutability semantics**: Once an attestation is written, can it be amended? JSONL is naturally append-only; YAML/Markdown allows in-place edits. Should we enforce append-only semantics even in YAML (e.g., entries can be added but not modified)?

5. **Status values**: Just `pass`/`fail`/`skip`? Or richer states like `pass-with-caveats`, `blocked`, `superseded`?

6. **How does this relate to existing signoff sections?** The plan-work-completion-signoff skill currently appends `## Completion Signoff` sections directly into plan docs. Should manifests replace those inline sections, complement them, or be generated from them?
