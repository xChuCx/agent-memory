# Spike S4 — SQLite FTS5 Incremental Update Pattern

**Status:** Validated after one round of fixes. Decision: **GO**.
**Started:** 2026-05-26
**Closed:** 2026-05-26
**Goal:** Confirm SQLite FTS5 supports per-section incremental updates in **<10ms on a 1000-section index** (the v0.4.1 plan §3 S4 exit criterion), and that BM25 ranking + `snippet()` previews work for the search path.

## Decision: GO

The pattern works, with a critical caveat baked into the documented setup: **WAL + synchronous=NORMAL is required**, not optional. With the default journal mode SQLite's per-commit fsync costs ~12ms on Windows and blows the budget. The spike caught this on the first run; after applying the standard production PRAGMAs the per-update cost dropped to **~700µs** — 14× under the budget. The pattern is approved for `internal/index/` in M2.

## How to validate

```powershell
go mod tidy
go test -v ./spikes/s4-fts5-incremental/...
```

Expected (paraphrased):

```
=== RUN   TestIncrementalUpdatePerformance
    index_test.go: 10 incremental updates on 1000-section index:
    index_test.go:   avg=<X>ms  max=<Y>ms  total=<Z>ms
--- PASS: TestIncrementalUpdatePerformance
=== RUN   TestIncrementalUpdateCorrectness
--- PASS: TestIncrementalUpdateCorrectness
=== RUN   TestBM25Ranking
    index_test.go: top-3 for 'refresh token rotation oauth':
      [0] modules/auth.md/best-match  score=<...>
--- PASS: TestBM25Ranking
=== RUN   TestSnippetExtraction
    index_test.go: snippet: <preview>
--- PASS: TestSnippetExtraction
=== RUN   TestIndexSize
    index_test.go: index size for 1000 sections: <N> bytes (<N> bytes/section)
--- PASS: TestIndexSize
=== RUN   TestFullSeedBaseline
    index_test.go: full seed: 1000 sections in <T> (avg <T/N>/section)
--- PASS: TestFullSeedBaseline
PASS
```

## Method

See [spikes/s4-fts5-incremental/README.md](../../spikes/s4-fts5-incremental/README.md). Six tests:

| Test | Purpose |
|---|---|
| `TestIncrementalUpdatePerformance` | Exit criterion: 10 UPSERTs on 1000-section index, `avg < 10ms`. |
| `TestIncrementalUpdateCorrectness` | After UPSERT, queries reflect new content; `memory_sections` row count is unchanged (UPSERT, not duplicate). |
| `TestBM25Ranking` | A targeted "best match" row beats synthetic noise on a focused query. |
| `TestSnippetExtraction` | FTS5 `snippet()` returns preview with markers. |
| `TestIndexSize` | Informational — bytes per section. |
| `TestFullSeedBaseline` | Informational — full N=1000 seed time. |

Update pattern (FTS5 has no `ON CONFLICT`):

```sql
BEGIN;
  DELETE FROM memory_search WHERE file = ? AND section_id = ?;
  INSERT INTO memory_search (...) VALUES (...);
  INSERT INTO memory_sections (...) VALUES (...)
    ON CONFLICT (file, section_id) DO UPDATE SET ...;
COMMIT;
```

Single transaction, three statements per affected section.

## Findings (running notes)

### 2026-05-26 — Initial implementation

Code:

- `spikes/s4-fts5-incremental/index.go` (~150 lines) — schema, Open, InsertSection, UpsertSection, Search, CountSections.
- `spikes/s4-fts5-incremental/fixtures.go` (~50 lines) — synthetic `Generate(n)` with overlapping keyword vocabulary across 8 files.
- `spikes/s4-fts5-incremental/index_test.go` (~200 lines) — 6 tests.

Approach choices, captured in [sqlite-fts5-shadow-index.md](../patterns/sqlite-fts5-shadow-index.md):

- `modernc.org/sqlite` (pure-Go, CGo-free, FTS5 by default).
- Driver name `"sqlite"`, not `"sqlite3"`.
- `SetMaxOpenConns(1)` to match v0.4.1 §11 single-writer model.
- Default journal mode (DELETE) for the spike; WAL is M2 polish.
- BM25 via FTS5's built-in `bm25()` function, sorted ascending (lower = better).
- Custom ranking multipliers (scope, freshness, archive penalty) applied in Go post-query.

go.mod dependency added: `modernc.org/sqlite` (version resolved by `go mod tidy`).

### 2026-05-26 — First run found two defects

Resolved `modernc.org/sqlite` version: **v1.34.0**.

Results:

```
TestIncrementalUpdatePerformance  FAIL  avg=11.6ms  exceeds 10ms exit criterion
TestIncrementalUpdateCorrectness  FAIL  expected 1000 sections, got 1001
TestBM25Ranking                   PASS  best-match top-1, score=-31.0943
TestSnippetExtraction             PASS  "[authentication] in the context of [authentication]..."
TestIndexSize                     PASS  884736 bytes for 1000 sections (~865 bytes/section)
TestFullSeedBaseline              PASS  full seed 1000 in 11.69s (11.69ms/insert)
```

**Defect 1 — performance: per-transaction fsync bottleneck (real finding).**

The per-update measurements were tightly clustered around 10-12ms with no variance, and the *full seed baseline* showed the exact same 11.69ms per INSERT. This rules out FTS5-specific cost: the bottleneck is the COMMIT fsync.

Default SQLite settings (`journal_mode=DELETE`, `synchronous=FULL`) require a full database fsync on every COMMIT. On Windows NTFS, that costs ~12ms per call, which is exactly what we measured.

**Fix:** enable `journal_mode=WAL` and `synchronous=NORMAL`. WAL writes to a separate log file; commits don't need a full database fsync, only the WAL is fsync'd, and `synchronous=NORMAL` lets even those WAL fsyncs be batched. Standard SQLite production setting. On crash, at most the last few transactions can be lost, but the database remains consistent — fine for a *derived, rebuildable shadow index*.

Applied via connection URI so the PRAGMAs are re-applied on any new connection database/sql opens:

```go
uri := fmt.Sprintf("%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
db, err := sql.Open("sqlite", uri)
```

Expected new numbers after WAL: 1-3ms per update, 1-3s full seed.

**Defect 2 — test bug: hardcoded (file, section_id) didn't match Generate's distribution.**

`Generate` distributes sections round-robin across 8 files (`modules/auth.md`, `modules/payments.md`, ...). `section-0042` actually lives in `modules/search.md`, not `modules/auth.md`. My TestIncrementalUpdateCorrectness UPSERT used the wrong file, which created a new row in `modules/auth.md` instead of updating the existing one. Row count went from 1000 to 1001.

The performance test had the same flavor of bug (used non-existent section IDs in `modules/auth.md`), so it was actually measuring *insert* performance not *upsert*. Practically the same fsync cost, but conceptually wrong.

**Fix:** Exposed `GeneratedFiles`, `FileForIndex`, `SectionIDForIndex` in `fixtures.go`. Tests now compute the correct (file, section_id) pair from the index, so UPSERTs hit existing rows. Performance test also added a post-loop assertion that row count remains exactly N.

### 2026-05-26 — After fixes (validated)

`go test -v ./spikes/s4-fts5-incremental/...` output:

```
TestIncrementalUpdatePerformance: PASS  avg=700.15µs  max=1.0006ms  total=7.0015ms
TestIncrementalUpdateCorrectness: PASS
TestBM25Ranking:                  PASS  best-match top-1, score=-31.0943
TestSnippetExtraction:            PASS
TestIndexSize:                    PASS  831488 bytes for 1000 sections (~831 bytes/section)
TestFullSeedBaseline:             PASS  full seed 1000 in 269.93ms (avg 269.93µs/insert)
```

Every check against the exit criterion is met with significant margin:

| Metric | Budget | First run | After WAL fix | Headroom |
|---|---|---|---|---|
| Per-update avg | <10ms | 11.6ms ❌ | **700µs** ✅ | 14× |
| Full seed 1000 sections | (informational) | 11.7s | **270ms** | 43× speedup |
| Index size per section | (informational) | 885 bytes | 831 bytes | — |
| BM25 best-match top-1 | required | PASS | PASS | — |
| Snippet markers | required | PASS | PASS | — |

The individual update timings cluster around 1ms with several `0s` entries — those are Windows `time.Now()` granularity artifacts when the underlying operation completes inside a single clock tick. The true per-update cost is somewhere between 600µs and 1ms; both well under budget.

## Decision outcome

**GO.** The incremental update pattern is validated. M2 `internal/index/` adopts it directly. Two non-negotiable configuration choices are now documented in [sqlite-fts5-shadow-index.md](../patterns/sqlite-fts5-shadow-index.md):

1. `journal_mode=WAL` and `synchronous=NORMAL` applied via the connection URI on every open.
2. `SetMaxOpenConns(1)` to match the single-writer concurrency model.

Both are spike-confirmed requirements, not optimizations.

## Next steps after GO

1. Move spike patterns into `internal/index/` during M2.
2. Add `memory_docs` table for per-file ranking signals (freshness, archive flag, etc.).
3. Wire the diff-and-upsert logic into the propose_update pipeline.
4. Implement the Go-side ranking multipliers per design doc §20.4.
5. Optionally enable WAL mode for read concurrency.

## Next steps if NO-GO on performance

1. Inspect the timing distribution — is the variance high (occasional GC pause) or is the average genuinely slow?
2. Enable WAL mode and rerun.
3. Profile with `pprof` to see where the time goes (DELETE, INSERT, transaction commit).
4. If pure-Go SQLite is genuinely the bottleneck, evaluate a CGo binding (`mattn/go-sqlite3` or `github.com/ncruces/go-sqlite3`) — but this fights the design's "no CGo" preference.
5. Worst case: relax the 10ms budget to a measured value, document why, and ensure the perceived UX of `propose_update` remains acceptable.
