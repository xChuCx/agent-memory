# Pattern: Archival Operations (M4)

**Status:** Implemented in [`internal/memory/operations.go`](../../internal/memory/operations.go) (the three ops) + [`internal/memory/update.go`](../../internal/memory/update.go) (orchestrator wiring).
**Owner:** `internal/memory/` (M4 — Release 0.2 acceptance gate).
**Tracks design:** [Design Doc v0.4.1 §15.8–15.10](../../agent-memory-design-doc-v0.4.1.md).

## Problem

The five MVP operations (`create_file`, `replace_section`,
`append_section`, `append_to_section`, `replace_section_content`) all
*add* or *modify* content within a single file. They have no answer for
the natural lifecycle question: **what happens when a section is no
longer current?**

Three needs, all archive-first (content is never silently destroyed):

1. **`archive_section`** — the section is superseded but worth keeping a
   pointer to. Copy it to `archive/`, leave a stub in place.
2. **`remove_section`** — the section is irrelevant, not even as a
   pointer. Copy it to `archive/`, then splice it out entirely.
3. **`rename_heading`** — the heading text is wrong but the section's
   identity (its `@id`) and body are fine. Change only the heading line.

## The multi-file challenge

`archive_section` and `remove_section` are the project's first
**multi-file operations**: they write to the source file AND create a
brand-new archive file. The existing `Operation` interface only models
a single-file splice:

```go
Plan(src []byte) (agentmd.SpliceOp, error)  // one file, one splice
```

Rather than overhaul the interface (and force all five existing ops to
change), M4 adds an **optional** interface the orchestrator type-asserts
for:

```go
type ExtraFileProducer interface {
    // ExtraFiles computes additional files this op creates, derived
    // from src — the primary file's bytes BEFORE this op's splice runs.
    ExtraFiles(src []byte) ([]ExtraFile, error)
}
```

Only `archive_section` and `remove_section` implement it. The five
original ops are untouched. The orchestrator's per-file loop does:

```
for each op on this file:
    if op is ExtraFileProducer:
        extras += op.ExtraFiles(cur)   // cur = bytes BEFORE the splice
    splice = op.Plan(cur)
    cur = Splice(cur, splice)
```

Calling `ExtraFiles(cur)` *before* the splice is essential: the archive
content is the section's bytes as they exist now, before
`archive_section` overwrites them with the stub (or `remove_section`
deletes them).

## Where extras get validated

A dedicated orchestrator pass (step 6.5) runs after all source-file
planning. Each extra archive file is treated as a brand-new file:

1. `ValidateMemoryPath` — must stay inside `.agent-memory/`.
2. `CategoryForPath` — must resolve to a category; not `server_managed`.
3. **Must not already exist on disk** → `archive_exists` rejection.
   Archive files are write-once.
4. **Must not be produced twice** in the same proposal → `archive_exists`.
5. `ValidateMarkdown` + secret/PII scan — same gate as any written file.

Validated extras are merged into `postState` + `fileOrder`, so the
staging writer and the apply path handle them uniformly with the
source-file edits. A synthetic `opCat` records the archive file's
category so re-indexing can resolve it without a primary op.

## Drift targets

`archive_section` and `remove_section` each declare two
`OperationTarget`s:

| Target | Policy | Meaning |
|--------|--------|---------|
| source file + section_id | `require_section_content_match` | the section we're archiving must be unchanged since stage time |
| archive_path | `require_file_absent` | the destination must not appear between stage and apply |

`rename_heading` declares one target on the source section with
`require_section_resolvable` (not content-match): rename only touches the
heading line, found by ID, so a body that grew since staging doesn't
block the apply.

## Write-once enforcement

Two layers:

1. **Primary-path check** (orchestrator step 4): a mutating op whose
   primary target is an existing file in a `write_once: true` category
   (i.e. `archive/`) → `write_once_violation`. This stops an agent from
   `replace_section`-ing an existing archive file.
2. **Extra-file check** (step 6.5): the archive destination must not
   already exist → `archive_exists`. Plus the `require_file_absent`
   drift target re-verifies this at apply time, closing the
   stage→apply race.

## Always-stage rule

Per design §15.8/§15.9, `archive_section` and `remove_section` are
**always staged**, regardless of the intent's manifest routing:

```go
if final.Mode == ApprovalApply && containsArchivalOp(ops) {
    final.Mode = ApprovalStage
    final.Reason += "; forced to stage: ... always staged (§15.8/§15.9)"
}
```

Archiving is durable and removal destroys source content — both warrant
human review even if the agent used an apply-routing intent. The
`Routing.Reason` records the forced override so the response is
self-explaining.

`rename_heading` follows normal intent routing (it's cosmetic and
ID-preserving — no forced stage).

## Provenance still applies

Archiving a decision touches `decisions.md`, whose category requires
provenance. So `archive_section` on a decision needs `sources`. This is
intentional: changing a durable file — even to archive part of it — is a
provenance-bearing act. The orchestrator computes provenance against the
dominant (first op's) category as usual.

## rename_heading specifics

- The splice covers only `[sectionStart, endOfHeadingLine)`. The `@id`
  anchor sits on the *next* line and is never touched.
- Level changes are constrained to ±1 of the current level (design
  §15.10) to prevent restructuring the document tree. `new_heading_level: 0`
  (or omitted) keeps the current level.
- A level jump beyond ±1 is a `plan_failed` rejection with an explanatory
  message.

## What M4 does NOT do

- **No un-archive / restore op.** Recovering an archived section is a
  manual `fetch` of the archive file + a fresh `append_section`.
- **No bulk archive.** One section per op; multi-section cleanup is
  multiple ops in one proposal (each archive_path must be distinct).
- **No automatic archive-path naming.** The agent supplies
  `archive_path`; the server doesn't invent date-stamped names. (A future
  `compact` command — design §17.2 — may add convenience naming.)
- **No cross-file move of arbitrary content.** ExtraFiles is scoped to
  the archive-copy use case, not a general "write to N files" primitive.

## References

- [Design Doc v0.4.1 §15.8–15.10](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.4 M4](../../agent-memory-implementation-plan.md).
- [Pattern: propose_update Pipeline](propose-update-pipeline.md) — the
  orchestrator these ops plug into.
- [Pattern: Staging Lifecycle](staging-lifecycle.md) — where the
  always-staged archival proposals land.
