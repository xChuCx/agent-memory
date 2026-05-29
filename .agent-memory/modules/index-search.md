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

`query.go` runs FTS5 BM25 (lower score = better). `ranking.go`'s
`ApplyRankingSignals` multiplies scores by documented signals and sorts
best-first. Implemented: ScopeBoost ×2.0, FreshBoost ×1.5, ArchivePenalty
×0.4, StalePenalty ×0.6. Multipliers are package constants (mirrors the
Jaccard threshold decision). Not-yet-wired signals (design §20.4):
active-branch ×1.3, decision/pitfall-referencing-changed-files ×1.4,
low-confidence ×0.8.
**Sources:** internal/index/ranking.go, internal/index/query.go
