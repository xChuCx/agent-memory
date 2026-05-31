# Pattern: Section-aware git merge driver

**Status:** Implemented in [`internal/markdown/merge.go`](../../internal/markdown/merge.go) (logic), [`internal/cli/merge_driver.go`](../../internal/cli/merge_driver.go) (CLI), [`internal/git/merge_driver.go`](../../internal/git/merge_driver.go) (registration).
**Owner:** `internal/markdown/` + `internal/git/` (M7).
**Tracks design:** [Design Doc v0.4.1 §24.5](../../agent-memory-design-doc-v0.4.1.md); Implementation Plan §11 (M7).

## Problem

`.agent-memory/` is committed to the repo, so a team shares memory through
git — `git pull` *is* the sync. The one thing git doesn't handle well:
when two branches both append to the same memory file (two developers each
record a different decision in `decisions.md`, or a pitfall in
`pitfalls.md`), git's line-based 3-way merge sees overlapping edits at the
end of the file and emits `<<<<<<<` conflict markers — even though there's
no real conflict: both sections should simply be kept.

## Solution

A custom git merge driver that merges these files **by section** instead of
by line. Registered per design §24.5:

```
# .agent-memory/.gitattributes   (committed)
*.md merge=agent-memory

# .git/config   (per-clone, set by `merge-driver --install`)
[merge "agent-memory"]
    name = agent-memory section-aware merge
    driver = agent-memory merge-driver %O %A %B %P
```

git substitutes `%O %A %B %P` = ancestor / ours / theirs / path; the driver
writes the merged result to `%A` and exits non-zero if conflicts remain.

### `markdown.Merge3Way(base, ours, theirs) → (result, conflicted, warnings)`

Each file is parsed into a tree of sections keyed by `@id` (or, un-anchored,
by heading text + level + occurrence). The merge is **recursive** and
**byte-preserving** — a kept section's bytes are copied verbatim from the
side that owns them, so nothing is reflowed:

| Situation | Result |
|---|---|
| Section unchanged, or changed on **one** side | take that version |
| Section **added** on one side only | keep it — *clean union* (the common case) |
| Section changed on **both** sides, differently | `<!-- @merge-conflict @id … -->` block wrapping both versions; `conflicted=true` |
| Section **deleted** on one side | keep the surviving version + a warning (memory is never silently lost) |
| Section deleted on **both** sides | drop it (both agree) |

Order is deterministic: base sections in base order, then ours-only
additions, then theirs-only. Children merge recursively, so the file
header (the `# Title` + intro) is preserved while the entries beneath it
union.

A conflict block keeps the familiar git markers inside a scoped comment so
a reviewer knows exactly which section diverged:

```
<!-- @merge-conflict @postgres — resolve and delete these markers -->
<<<<<<< ours
…ours version…
=======
…theirs version…
>>>>>>> theirs
<!-- /@merge-conflict -->
```

## Usage

```bash
# once per clone (git config is local and can't be committed)
agent-memory merge-driver --install

# git invokes this form itself during a merge; you rarely type it
agent-memory merge-driver %O %A %B %P
```

`--install` writes the committed `.agent-memory/.gitattributes` and the
local git config. A teammate who clones the repo already has the
`.gitattributes`; they run `--install` once to register the driver locally.
`memory.status` reports `merge_driver_installed` by probing git config.

## Design choices

- **Opt-in.** Not every team wants a custom driver; the default remains
  git's built-in merge plus the section-per-topic discipline (each topic a
  stable `@id`, so edits to different sections already merge cleanly). The
  driver removes the residual append-at-EOF conflicts.
- **Never lose memory.** A delete-vs-edit keeps the surviving content and
  warns, rather than dropping a section someone may still want. Both-sides
  delete is honoured.
- **Parse failure → fall back.** If a file can't be parsed into sections,
  the driver errors (non-zero), so git leaves it for manual resolution
  rather than guessing.
- **Logic in `internal/markdown`.** It's pure section/byte manipulation
  over the existing engine; `internal/git` only shells out to register the
  config, and the CLI wires the two together.

## Tests

- [`internal/markdown/merge_test.go`](../../internal/markdown/merge_test.go) — identical round-trip, both-append union (+ order), one-sided edit, both-sided conflict (scoped marker + both versions), delete-keeps-surviving (+ warning), both-delete honoured.
- Verified end-to-end against real `git merge`: two branches appending different decisions to `decisions.md` merge cleanly (no markers, both kept) — the M7 acceptance gate.

## References

- [Design Doc v0.4.1 §24.5](../../agent-memory-design-doc-v0.4.1.md) — merge strategy for durable memory.
- [byte-preserving-engine.md](./byte-preserving-engine.md) — the section model this builds on.
- `git help attributes` — custom merge drivers.
