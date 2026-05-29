# Pattern: Context-Pack Near-Duplicate Suppression (Jaccard)

**Status:** Implemented in [`internal/memory/jaccard.go`](../../internal/memory/jaccard.go); wired into `buildSearchPack` in [`internal/memory/fetch.go`](../../internal/memory/fetch.go).
**Owner:** `internal/memory/` (M2 retrieval, polish).
**Tracks design:** [Design Doc v0.4.1 §15.1 step 8 / §20.5 step 6](../../agent-memory-design-doc-v0.4.1.md).

## Problem

`memory.fetch_context` returns a **budgeted** Markdown pack: a fixed
character budget split across the highest-ranked sections. FTS5 search
plus section-level chunking can surface two sections that say almost the
same thing — the same pitfall copied into two module files, a decision
restated in `current.shared.md`, a convention duplicated after a refactor.

Including both is pure waste: the caller pays budget twice for one idea
and a genuinely distinct section gets pushed out. The design doc calls for
collapsing semantic overlap *after* ranking and *before* budget
enforcement, keeping the higher-scoring representative.

## Solution

A dependency-free set-Jaccard over word tokens, applied as a filter stage
inside the search-pack assembler.

```go
const dedupeJaccardThreshold = 0.85 // design §20.5: "Jaccard > 0.85"

func tokenize(s string) map[string]struct{}                       // lowercase [letter|digit]+ runs, as a set
func jaccardSimilarity(a, b map[string]struct{}) float64          // |A∩B| / |A∪B|, in [0,1]
func isNearDuplicate(cand map[string]struct{}, accepted []map[string]struct{}) bool
```

`tokenize` lowercases and splits on anything that isn't a letter or digit,
so Markdown punctuation, `<!-- @id -->` markers, and whitespace fall away.
Set (not multiset) semantics match Jaccard: a repeated word counts once.

`jaccardSimilarity` iterates the smaller set for the intersection and
defines similarity as 0 when either set is empty — an empty section must
never suppress a non-empty one.

## Where it runs in the pipeline

```text
buildSearchPack:
  1. Index.Search(query)                       ← FTS5 / BM25
  2. ApplyRankingSignals(...)                  ← scope/fresh/archive/stale; sorts best-first
  3. (always-prepend branch-local + shared current state)
  4. for each ranked result, in order:
       resolve section body bytes
       tokens := tokenize(body)
       if isNearDuplicate(tokens, acceptedTokens):   ← THIS STAGE
           omit "near-duplicate of higher-ranked section"; continue
       if would exceed budget:
           omit "budget exhausted"; continue
       append to pack; acceptedTokens = append(acceptedTokens, tokens)
```

`acceptedTokens` holds the token sets of sections **already written into
the pack**, in rank order. Because `ApplyRankingSignals` sorts best-first
(FTS5 BM25 is "lower is better"), the first member of a duplicate cluster
we accept is the higher-ranked one — so any later section that matches it
above the threshold is, by construction, the lower-ranked duplicate and is
dropped. This realizes "keep higher-scoring" without a separate clustering
pass.

### Dedup before budget, interleaved in one pass

The design lists dedup (step 6) before budget (step 7). We honor that
ordering by checking duplication first, then budget, in the same loop. A
section dropped for **budget** is not added to `acceptedTokens` (it never
made the pack), so it can't suppress a later distinct section — and if a
later, lower-ranked duplicate happens to fit the budget the higher one
couldn't, including it is strictly better than emitting nothing. The
invariant that matters holds either way: **the pack itself never contains
a near-duplicate pair.**

## Why tokenize the body, not the rendered chunk

Each included section is prefixed with a synthetic
`<!-- @file: <path> @id: <id> score: <n> -->` header. That header's score
is per-section and would pollute the token set, so dedup tokenizes the raw
section body only.

## Why not MinHash / LSH / embeddings

At the project's scale (≤ 50 search results per query) the O(n²) pairwise
comparison over small token sets is negligible. MinHash/LSH buys nothing
until the candidate set is orders of magnitude larger, and embeddings
would add a model dependency and a similarity that's harder to reason
about and test. The plain Jaccard is deterministic, explainable in a log
line, and unit-testable with hand-computed expectations.

## Threshold as a constant, not manifest config

`0.85` is fixed in the design doc, and the sibling retrieval tunables —
the ranking multipliers in [`internal/index/ranking.go`](../../internal/index/ranking.go)
(`ScopeBoost`, `ArchivePenalty`, …) — are package constants too. Keeping
the threshold a constant matches that precedent and avoids manifest
surface (defaults, validation, migration) for a value nobody is expected
to tune.

## Tests

[`internal/memory/jaccard_test.go`](../../internal/memory/jaccard_test.go):

- `tokenize` — lowercasing, set dedup, punctuation/Markdown stripping, empties.
- `jaccardSimilarity` — identical (1.0), disjoint (0.0), partial (2/6), subset, empty operands, symmetry.
- `isNearDuplicate` — a one-word edit stays above threshold; unrelated text stays below; empty accepted set never matches.
- `BuildContextPack` end-to-end — two near-identical sections in different files both match the query; exactly one lands in the pack, the other is reported `omitted` with a `near-duplicate` reason.

## References

- [Design Doc v0.4.1 §15.1 / §20.4 / §20.5](../../agent-memory-design-doc-v0.4.1.md).
- [sqlite-fts5-shadow-index.md](./sqlite-fts5-shadow-index.md) — the search + ranking that feeds this stage.
- Jaccard index — standard set-similarity metric.
