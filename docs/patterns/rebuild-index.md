# Pattern: rebuild-index (M7)

**Status:** Implemented in [`internal/cli/rebuild_index.go`](../../internal/cli/rebuild_index.go); core logic in [`internal/index/rebuild.go`](../../internal/index/rebuild.go) (predates M7, reused).
**Owner:** `internal/cli/` (M7, Release 0.2).
**Tracks design:** [Design Doc v0.4.1 §29](../../agent-memory-design-doc-v0.4.1.md) (FTS5 shadow index lifecycle).

## Problem

The FTS5 shadow index at `.agent-memory/meta/index.sqlite` is **derived
state**. The canonical source of truth is the Markdown files; the
index exists to make `memory.fetch_context` queries fast. Three of the
common ways the index falls out of sync:

1. **SQLite file damage** — power loss mid-write, a non-WAL-aware tool
   pokes the file, a checksum-mismatched WAL replay fails to recover.
2. **Markdown edits bypassing the pipeline** — a user manually `vim`s
   a decisions.md file. The incremental index path only fires from
   `propose_update` / `apply`; manual edits drift.
3. **Schema change** — adding a new category or changing a file glob
   in `schema.yaml` means existing files need re-categorising.

For all three, the right answer is the same: nuke the index and walk
the canonical Markdown files from scratch. `agent-memory
rebuild-index` is the one-command escape hatch.

## Core operation already existed

`internal/index/rebuild.go`'s `RebuildAll(ctx, memDir, schema, opts)`
has done the actual work since M2 — it's what fetch's auto-rebuild
calls on a fresh index. The M7 deliverable is purely the CLI surface
around it:

```
agent-memory rebuild-index [--root DIR] [--clobber] [--no-assign-ids] [--json]
```

with the same shape as every other agent-memory CLI command: `--root`
override, `--json` output, locked execution.

## Two modes: DELETE vs `--clobber`

```
default   → idx.RebuildAll() which DELETE-FROMs the three tables, walks
             memDir, repopulates. Keeps the SQLite file in place. Fast.

--clobber → os.Remove(meta/index.sqlite*), then Open fresh, then
             RebuildAll. Slower, allocates a new file. Use only when
             the SQLite file ITSELF is damaged (the default DELETE-FROM
             needs a working file to operate on).
```

The `--clobber` path also removes `-wal`, `-shm`, and `-journal`
siblings to avoid a stale WAL replaying against a fresh database file.

For 99% of "the index looks stale" cases, the default (DELETE-FROM) is
sufficient and faster. `--clobber` is the genuine corruption case —
typically signalled by SQLite errors like
`database disk image is malformed` from the underlying driver.

## Locking

`rebuild-index` acquires the cross-process advisory lock at
`.agent-memory/meta/lock` for the duration of the rebuild. Rationale:

- A concurrent `propose_update` / `apply` writes via the incremental
  path (`UpsertSections`, `UpsertFile`). Between `wipeAll` and the
  walk's reindex, the tables are empty — a concurrent write would
  succeed but its rows would be wiped out by the next iteration.
- A concurrent fetch is read-only over the .md files but reads the
  index for queries. A query during the empty window returns zero
  results — wrong but not catastrophic.

The lock prevents both. If another writer holds the lock,
`rebuild-index` returns a clear error rather than blocking forever or
silently corrupting.

## `--assign-ids`: anchor injection on rebuild

Categories with `section_id_required: true` in the schema demand every
section carry an `<!-- @id: ... -->` anchor. A user-written file may
lack them; the FTS5 index can still hold the rows but `FindByID` can't
locate them later.

By default, `rebuild-index --assign-ids` (which is ON unless explicitly
disabled) runs `internal/markdown.AssignMissingIDs` on every file in a
category that requires IDs. Modified bytes are written back atomically
before indexing. Idempotent — files that already have anchors are not
touched.

Pass `--no-assign-ids` if you want a purely read-only rebuild (e.g.,
forensic inspection of how the index would look without changing files
on disk).

## What rebuild-index does NOT do

- **No schema migrations.** It re-reads the schema and re-categorises
  files, but it doesn't transform Markdown content to match new
  schema constraints. A new `required_field` in the SectionSchema
  won't cause the rebuild to inject the field; rebuild will index
  what's there and let the next propose_update fail validation on
  noncompliant sections.
- **No .md content repair.** If a file is malformed (corrupt UTF-8,
  unterminated code fence), `RebuildAll` skips it with an error
  bubbling up. The fix is to repair the file, not the index.
- **No git interaction.** The rebuild touches only
  `.agent-memory/meta/index.sqlite*` and (with `--assign-ids`) the
  .md files that needed anchors. It does NOT auto-stage or commit
  even when `manifest.git.auto_stage_changes` is true — index files
  are usually `.gitignore`d, and anchor injections are part of the
  user's manual workflow.
- **No staging cleanup.** Staged proposals at
  `.agent-memory/staging/` are untouched. Use `agent-memory sweep`
  for that.

## When the auto-rebuild kicks in instead

`agent-memory fetch` checks `idx.CountSections(ctx)` on every call; if
zero, it auto-runs `RebuildAll`. This handles the "first call after
init" case without any user action. The explicit `rebuild-index` CLI
is for cases where the count is non-zero but the contents are wrong —
something fetch's check can't detect.

## CLI output

Human form:

```
$ agent-memory rebuild-index
Index rebuilt in 0.18s
  files:    7
  sections: 23
  ids:      missing anchors injected where required
```

With `--clobber`:

```
$ agent-memory rebuild-index --clobber
Index rebuilt in 0.21s
  files:    7
  sections: 23
  mode:     clobber (SQLite file removed and recreated)
  ids:      missing anchors injected where required
```

JSON (for scripts / agents):

```json
{
  "files_indexed": 7,
  "sections_indexed": 23,
  "duration_seconds": 0.184,
  "assigned_ids": true
}
```

## References

- [Design Doc v0.4.1 §29 (FTS5 lifecycle)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.7 M7](../../agent-memory-implementation-plan.md).
- [Pattern: Cross-Process Locking](cross-process-locking.md) — the
  lock rebuild holds for the duration.
- [Pattern: Byte-Preserving Engine](byte-preserving-engine.md) —
  AssignMissingIDs uses the same splice primitive.
