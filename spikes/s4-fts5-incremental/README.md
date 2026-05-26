# Spike S4 — SQLite FTS5 Incremental Update Pattern

**Purpose:** Confirm that we can update individual sections in the SQLite FTS5 shadow index in **under 10ms** on a 1000-section index, without rebuilding from scratch. This is the central performance assumption behind the v0.4.1 design (§20.3) — every `propose_update` triggers an incremental index refresh, and that refresh must be bounded by the number of affected sections, not the index size.

**Why this matters:** If FTS5 incremental updates are not fast enough, every meaningful agent action becomes a perceived stall, and the design's "incremental from day one" claim (M2 acceptance gate) collapses.

## How to run

```powershell
go mod tidy
go test -v ./spikes/s4-fts5-incremental/...
```

Expected:

```
=== RUN   TestIncrementalUpdatePerformance
    index_test.go: 10 incremental updates on 1000-section index:
    index_test.go:   avg=<Xms>  max=<Yms>  total=<Zms>
--- PASS: TestIncrementalUpdatePerformance
=== RUN   TestIncrementalUpdateCorrectness
--- PASS: TestIncrementalUpdateCorrectness
=== RUN   TestBM25Ranking
    index_test.go: top-3 for 'refresh token rotation oauth':
      [0] modules/auth.md/best-match  score=<...>
--- PASS: TestBM25Ranking
=== RUN   TestSnippetExtraction
--- PASS: TestSnippetExtraction
=== RUN   TestIndexSize
    index_test.go: index size for 1000 sections: <N> bytes (<bytes/section>)
--- PASS: TestIndexSize
=== RUN   TestFullSeedBaseline
    index_test.go: full seed: 1000 sections in <T> (avg <T/N>/section)
--- PASS: TestFullSeedBaseline
PASS
```

## What the tests do

| Test | Verifies |
|---|---|
| `TestIncrementalUpdatePerformance` | **The exit criterion.** 10 sequential UPSERTs on a 1000-section index. Asserts `avg < 10ms`. Logs avg, max, and each individual duration. |
| `TestIncrementalUpdateCorrectness` | After UPSERT, FTS5 MATCH finds the updated row with the new content (and `memory_sections` row count stays at N — UPSERT, not duplicate INSERT). |
| `TestBM25Ranking` | A purpose-built "best match" row is ranked above synthetic noise for a targeted query. Confirms `bm25()` ordering works as expected. |
| `TestSnippetExtraction` | The FTS5 `snippet()` helper returns previews with match markers. |
| `TestIndexSize` | Informational — logs on-disk size and bytes-per-section for the 1000-section index. |
| `TestFullSeedBaseline` | Informational — full N=1000 seed time, for comparison against incremental updates. |

## Schema (from design doc §20.2)

```sql
CREATE VIRTUAL TABLE memory_search USING fts5(
    file, section_id, title, headings, content, tags,
    tokenize='porter unicode61'
);

CREATE TABLE memory_sections (
    file          TEXT    NOT NULL,
    section_id    TEXT    NOT NULL,
    heading       TEXT    NOT NULL,
    heading_level INTEGER NOT NULL,
    byte_start    INTEGER NOT NULL,
    byte_end      INTEGER NOT NULL,
    content_hash  TEXT    NOT NULL,
    PRIMARY KEY (file, section_id)
);
```

The spike omits `memory_docs` (per-file metadata) — it's not on the incremental-update hot path. M2 adds it back.

## Update pattern

FTS5 virtual tables don't support `ON CONFLICT`. The incremental pattern is:

```sql
BEGIN;
  DELETE FROM memory_search WHERE file = ? AND section_id = ?;
  INSERT INTO memory_search (...) VALUES (...);
  INSERT INTO memory_sections (...) VALUES (...)
    ON CONFLICT (file, section_id) DO UPDATE SET ...;
COMMIT;
```

Single transaction, three statements. The DELETE+INSERT for `memory_search` is the FTS5-canonical way; `memory_sections` uses standard UPSERT.

## Driver

`modernc.org/sqlite` — pure-Go port of SQLite, FTS5 enabled by default. CGo-free, which keeps cross-compilation simple (no `gcc` needed on Windows runners). Driver name is `"sqlite"` (singular, not `"sqlite3"`).

## See also

- [Pattern: SQLite FTS5 Shadow Index](../../docs/patterns/sqlite-fts5-shadow-index.md)
- [Spike S4 Results](../../docs/spikes/s4-results.md)
- [Design Doc v0.4.1 §20](../../agent-memory-design-doc-v0.4.1.md)
- [Implementation Plan §3 S4](../../agent-memory-implementation-plan.md)
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)
