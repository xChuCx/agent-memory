# Pattern: Fetch Ranking Signals

**Status:** Implemented in [`internal/index/ranking.go`](../../internal/index/ranking.go); content-level signals fed by [`internal/index/query.go`](../../internal/index/query.go) (`SearchResult.Content`) and [`internal/git/changed.go`](../../internal/git/changed.go).
**Owner:** `internal/index/` (retrieval).
**Tracks design:** [Design Doc v0.4.1 §20.4](../../agent-memory-design-doc-v0.4.1.md).

## Problem

FTS5 BM25 alone doesn't know which sections matter *to the agent right
now*. A stale archived note and a fresh decision about the file you're
editing can score the same on raw term frequency. `fetch_context` needs to
re-rank BM25 hits by project-aware signals before it spends the budget.

## Solution

`ApplyRankingSignals(results, RankingContext)` multiplies each hit's BM25
score by a set of documented constants, then sorts ascending (FTS5
convention: lower = better, so a boost makes the score *more* negative).
Multipliers compose and commute.

```go
// boosts (>1 → rank higher)            // penalties (<1 → rank lower)
ScopeBoost        = 2.0                  ArchivePenalty       = 0.4
FreshBoost        = 1.5                  StalePenalty         = 0.6
ActiveBranchBoost = 1.3                  LowConfidencePenalty = 0.8
ChangedRefBoost   = 1.4
```

### File-level vs content-level signals

| Signal | Multiplier | Keys off | Source of truth |
|---|---|---|---|
| Scope match | ×2.0 | result path ⊃ scope string | caller's `scope[]` |
| Fresh file | ×1.5 | `FreshFiles[path]` | freshness markers *(not yet populated)* |
| Archived | ×0.4 | path under `archive/` | path prefix |
| Stale file | ×0.6 | `StaleFiles[path]` | freshness markers *(not yet populated)* |
| **Active-branch reference** | **×1.3** | section body mentions the branch | `RankingContext.ActiveBranch` |
| **Decision/pitfall → changed file** | **×1.4** | `decisions.md`/`pitfalls.md` body cites an uncommitted file | `RankingContext.ChangedFiles` |
| **Low confidence** | **×0.8** | section's `Confidence:` is inferred/stale/unknown | section body |

File-level signals only need the path. The three content-level signals
(the last three rows) inspect `SearchResult.Content` — the full indexed
section body, now returned by `Search` alongside the snippet.

### The three signals added here

**Active-branch reference (×1.3).** A section whose body mentions the
current branch name is probably scoped to the work in flight. Generic
integration branches (`main`, `master`, `trunk`, `develop`, `dev`) and
names shorter than 3 chars earn *no* boost — almost everything lives on
`main`, so matching it would be pure noise.

**Decision/pitfall referencing a changed file (×1.4).** When you're
editing `internal/auth/session.go`, a decision or pitfall that cites that
path is exactly the prior art you want surfaced. The signal fires only for
`decisions.md` and `pitfalls.md` hits whose body contains a path from
`git status` (modified, added, deleted, renamed, or untracked — see
[`git.ChangedFiles`](../../internal/git/changed.go)).

**Low confidence (×0.8).** A section that declares `Confidence: inferred`
(or `stale` / `unknown`) is downweighted relative to a `confirmed` one.
`sectionConfidence` tolerates Markdown emphasis and bullet/blockquote
markers around the label (`**Confidence:** inferred`, `- Confidence:
stale`). Sections with no `Confidence:` field — most of them — are never
penalized.

## Plumbing

```
cli/fetch.go ─┐                         git.ActiveBranch(root) → Branch.Name
mcp/tools.go ─┤ build FetchDeps with    git.ChangedFiles(root) → ChangedFiles
              ▼
memory.BuildContextPack
              ▼ buildSearchPack
   index.Search(query)                  → []SearchResult{..., Content}
   index.ApplyRankingSignals(results, RankingContext{
       Scope, ActiveBranch, ChangedFiles, FreshFiles, StaleFiles})
   jaccard dedup → budget → pack
```

`ActiveBranch` and `ChangedFiles` are resolved by the **caller** (CLI /
MCP) and passed in `FetchDeps`, mirroring how `Branch` is already resolved
there. This keeps `BuildContextPack` a pure function of its deps — no git
shell-out hidden in the memory package — and trivially testable.

## Design choices

- **Constants, not manifest config.** The multipliers (and the Jaccard
  threshold) are package constants. A project isn't expected to tune them;
  keeping them out of the manifest avoids defaults/validation/migration
  surface. See the decision in `.agent-memory/decisions.md`.
- **Content returned from the index, parsed at rank time.** Rather than
  add `confidence`/`refs` columns + a schema migration, the signals parse
  the already-stored FTS `content`. At ≤50 hits/query the string scans are
  negligible, and there's nothing new to keep in sync.
- **Best-effort git.** A missing `git`, a non-repo root, or a clean tree
  just yields no branch/changed-file boost — never an error.

## Tests

- [`internal/index/ranking_test.go`](../../internal/index/ranking_test.go) — each new signal in isolation (boost applied / not applied / re-sorted), generic-branch suppression, module-vs-decision gating for the changed-file signal, and `sectionConfidence` / `isLowConfidence` field parsing.
- [`internal/git/changed_test.go`](../../internal/git/changed_test.go) — non-repo no-op, clean tree, modified + untracked (forward-slash, subdir).

## References

- [Design Doc v0.4.1 §20.4](../../agent-memory-design-doc-v0.4.1.md) — ranking signal catalogue.
- [context-pack-dedup.md](./context-pack-dedup.md) — the dedup stage that runs after ranking.
- [sqlite-fts5-shadow-index.md](./sqlite-fts5-shadow-index.md) — the BM25 search this re-ranks.
