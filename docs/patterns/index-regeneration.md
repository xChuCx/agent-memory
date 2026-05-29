# Pattern: Server-Maintained `index.md` Regeneration

**Status:** Implemented in [`internal/memory/index_gen.go`](../../internal/memory/index_gen.go); wired into the apply paths ([`update.go`](../../internal/memory/update.go), [`staging.go`](../../internal/memory/staging.go)), `init`, and `rebuild-index`.
**Owner:** `internal/memory/` (Release 0.2 acceptance gate — final 🔴).
**Tracks design:** [Design Doc v0.4.1 §10.1](../../agent-memory-design-doc-v0.4.1.md).

## Problem

The schema declares `index.md` as `server_managed` — agents may not
write it through `propose_update` (a write attempt is rejected with
`server_managed_category`). The design (§10.1) says the file "is
regenerated on every durable memory change, summarising file purpose,
freshness, and stale areas."

Through Release 0.1 / early 0.2, that regeneration never happened.
`init` wrote a one-time static stub and the server never touched the
file again. The result: a file the schema reserved for the server but
that the server treated as inert. This was the last red acceptance-gate
gap from the plan-vs-implementation audit.

## Solution

A deterministic generator + a regenerate-if-changed wrapper, invoked as
a side effect of every durable write.

```go
func BuildIndexContent(memDir string, sch *schema.Schema) ([]byte, error)
func RegenerateIndex(memDir string, sch *schema.Schema) (changed bool, err error)
```

`BuildIndexContent` walks the tree and produces the §10.1 structure:

```md
# Agent Memory Index
<!-- @generated: do not edit by hand; use `agent-memory rebuild-index` -->

## Always include
- local/current.<branch>.md — current local task state
- local/current.shared.md — cross-branch shared state
- conventions.md — build, test, style, workflow rules

## Topic map
- decisions.md — durable architecture/product decisions (3 active, 2 superseded)
- pitfalls.md — known traps (5 entries)
- modules/auth.md — module notes
- modules/payments.md — module notes

## Archive
- archive/ — 7 archived context(s), fetched only on strong match

## Freshness
Per-section freshness tracking is not yet enabled; no stale areas are flagged.
```

## Deterministic by design

`BuildIndexContent` puts **no wall-clock timestamp in the body**. The
output is a pure function of the tree's content (decision-status tallies,
pitfall counts, module names, archive count). Two consequences:

1. **No needless git churn.** `RegenerateIndex` compares the new content
   to what's on disk and writes only on a real difference. An apply that
   doesn't change the index's summary (e.g. an `append_to_section` that
   adds a bullet without changing the section count) leaves `index.md`
   untouched — no spurious "modified" in `git status`.
2. **Stable tests.** The same tree always yields the same bytes, so the
   generator is straightforward to assert against.

The design's example shows a "Last full validation: 2026-05-26" line.
We deliberately omit it: a volatile date would defeat both properties
above, and per-section freshness tracking (§20.3) — the mechanism that
would populate real stale areas — isn't implemented yet. The Freshness
section says so honestly rather than printing a misleading "all fresh".

## When regeneration fires

| Trigger | Call site | Notes |
|---------|-----------|-------|
| `agent-memory init` | `cli/init.go` | replaces the old static stub; the index reflects the seeded (empty) templates |
| apply-routing `propose_update` | `applyImmediately` (update.go) | after WriteAtomic + reindex, before auto-stage |
| staged `apply` | `ApplyStaged` (staging.go) | same position; uses the staged proposal's writes |
| `agent-memory rebuild-index` | `cli/rebuild_index.go` | regenerated alongside the FTS rebuild — the `@generated` comment even points users here |

All apply-path calls are **best-effort**: a regeneration failure never
rolls back the durable write. The bytes already landed; a stale index is
recoverable with `rebuild-index`. This mirrors the FTS re-index contract.

## Interaction with git auto-stage

When `RegenerateIndex` reports `changed == true` and `index.md` is
git-tracked, the apply path folds it into the auto-stage batch
(`appendUnique(fileOrder, "index.md")`). So a commit produced by
`manifest.git.auto_commit` captures the regenerated routing file
alongside the durable edit that triggered it — the index never drifts
behind the content in git history.

It's also re-indexed into the FTS shadow so a fetch query can surface
the routing file's summary.

## Why not regenerate inside `propose_update`'s validation phase

Regeneration is a **post-write side effect**, not part of the
propose/validate/stage pipeline. Reasons:

- It reads the *applied* on-disk state. During staging, nothing has
  landed yet — there's nothing new to summarise.
- It writes a `server_managed` file, which the pipeline is forbidden
  from doing for agent ops. Keeping regeneration outside the pipeline
  preserves that invariant cleanly: agents propose; the server maintains
  the index.

## What this does NOT do (yet)

- **No real freshness/stale tracking.** The Freshness section is a
  placeholder until per-section freshness markers (§20.3) exist. When
  they do, `BuildIndexContent` gains a stale-area pass.
- **No per-module summaries.** Modules are listed by path with a generic
  "module notes" label. Extracting a one-line purpose from each module's
  first paragraph is a future enhancement.
- **No decision titles.** The topic map counts decisions by status but
  doesn't enumerate them — `fetch_context` is the navigation tool for
  drilling in; the index is a map, not a directory listing.

## References

- [Design Doc v0.4.1 §10.1 (`index.md`)](../../agent-memory-design-doc-v0.4.1.md).
- [Pattern: propose_update Pipeline](propose-update-pipeline.md) — the apply paths this hooks into.
- [Pattern: rebuild-index](rebuild-index.md) — the command the `@generated` comment points at.
- [Pattern: Git Auto-Stage](git-auto-stage.md) — how the regenerated index joins a commit.
