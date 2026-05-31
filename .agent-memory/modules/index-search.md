# Module: internal/index
<!-- @id: module-index -->

The SQLite FTS5 shadow index over memory sections, plus query ranking.
CGo-free (`modernc.org/sqlite`).

## shadow index + incremental upsert
<!-- @id: index-shadow -->

`sqlite.go` / `incremental.go`: `memory_sections` mirrors parsed sections;
edits UPSERT (no duplicate rows). `Open` / `Init` / `RebuildAll` /
`CountSections`. The index is a cache — `rebuild-index` and first-use
auto-build reconstruct it from the Markdown files, which are the source of
truth. `RebuildOpts{AssignMissingIDs}` backfills `@id` anchors.
**Sources:** internal/index/sqlite.go, internal/index/incremental.go

## query + ranking signals
<!-- @id: index-ranking -->
`query.go` runs FTS5 BM25 (lower score = better) and returns the section
body as `SearchResult.Content`. `ranking.go`'s `ApplyRankingSignals`
multiplies scores by documented signals (all package constants) and sorts
best-first.

File-level: ScopeBoost x2.0, FreshBoost x1.5, ArchivePenalty x0.4,
StalePenalty x0.6. Content-level (design §20.4): active-branch x1.3
(suppressed on main/master), decision/pitfall-referencing-changed-files
x1.4 (via `git.ChangedFiles`), low-confidence x0.8 (inferred/stale/unknown).

Fresh/stale maps still await per-section freshness markers (§20.3).
**Sources:** internal/index/ranking.go, internal/index/query.go, internal/git/changed.go
