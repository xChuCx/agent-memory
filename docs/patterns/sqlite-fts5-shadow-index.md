# Pattern: SQLite FTS5 Shadow Index

**Status:** Implemented in [`internal/index/`](../../internal/index). Spike-validated in S4; production tests exercise the same WAL + incremental UPSERT pattern.
**Owner:** `internal/index/` (M2).
**Tracks design:** [Design Doc v0.4.1 §17, §20](../../agent-memory-design-doc-v0.4.1.md).

## Problem

The Markdown files in `.agent-memory/` are the source of truth, but a flat-file grep does not give us:

- Section-level chunking with byte-offset bookkeeping.
- Ranked retrieval (BM25) for `memory.fetch_context` queries.
- Metadata-aware filtering (freshness, scope, archive penalty).
- Incremental updates bounded by the number of changed sections, not the index size.

The shadow index serves these. It is **local, derived, gitignored, rebuildable** — the Markdown files remain canonical.

## Solution

SQLite with FTS5, one database file at `.agent-memory/meta/index.sqlite`. Two tables (a third, `memory_docs`, for per-file metadata is added in M2 for ranking signals):

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

`memory_search` is the FTS5 virtual table; `memory_sections` is a regular table keyed by `(file, section_id)`. The FTS5 row is what makes queries fast; the `memory_sections` row carries the byte offsets that `fetch_context` uses to *read the actual section bytes* from the source Markdown file (the index doesn't store full text for chunk assembly).

## Incremental update pattern

FTS5 virtual tables do not support `ON CONFLICT`. For each changed section, the update is:

```sql
BEGIN;
  DELETE FROM memory_search WHERE file = ? AND section_id = ?;
  INSERT INTO memory_search (file, section_id, title, headings, content, tags) VALUES (?, ?, ?, ?, ?, ?);
  INSERT INTO memory_sections (file, section_id, heading, heading_level, byte_start, byte_end, content_hash)
    VALUES (?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT (file, section_id) DO UPDATE SET
      heading = excluded.heading,
      heading_level = excluded.heading_level,
      byte_start = excluded.byte_start,
      byte_end = excluded.byte_end,
      content_hash = excluded.content_hash;
COMMIT;
```

Single transaction, three statements per affected section. The DELETE+INSERT for `memory_search` is the FTS5 canonical "upsert".

After a `propose_update` writes a file:

1. Re-parse only the affected file via the byte-preserving Markdown engine (returns the new `[]Section`).
2. Diff old vs new section IDs:
   - Sections in both with different `content_hash` → upsert above.
   - Sections only in new → insert.
   - Sections only in old → delete (DELETE from both tables).
3. Done. The index is consistent with the file.

This is **O(changed sections)**, not O(total sections in the file or repo).

## Search pattern

```sql
SELECT
    file,
    section_id,
    title,
    headings,
    snippet(memory_search, 4, '[', ']', '...', 16) AS snip,
    bm25(memory_search) AS score
FROM memory_search
WHERE memory_search MATCH ?
ORDER BY score
LIMIT ?;
```

- `bm25()` returns a score where **lower is better** (FTS5 convention). Sort ASC.
- `snippet()` returns a preview centred on the match. Arguments: column index, start marker, end marker, ellipsis, tokens.
- Custom ranking multipliers (scope boost, freshness, archive penalty per design doc §20.4) are applied in Go code after the SQL query — multiply / add against `bm25()` score there.
- **The MATCH argument is a sanitized query, never the raw user/agent
  string.** Fetch queries are natural language; passing them verbatim lets
  FTS5 metacharacters (`-`, `:`, `"`, `*`, `AND`/`OR`/`NEAR`) reach the
  query parser and crash with `SQL logic error` / `no such column`.
  `sanitizeFTSMatch` (query.go) extracts `[\p{L}\p{N}]+` runs and
  double-quotes each as a literal term, OR-joined so a multi-word query
  retrieves the best partial matches (BM25 ranks them; match-all would make
  natural-language queries return almost nothing); a query with no
  alphanumeric content becomes empty (no rows). The full section body
  (`content` column) is also selected so content-level ranking signals can
  inspect it.

## Federation: the `store` dimension (schema v2)

Federated "landscape" memory (see [federation-stores.md](federation-stores.md))
lets a repo reference shared external stores, synced read-only into
`meta/cache/stores/<name>/`. The index gained a `store` dimension so one shadow
index can hold the local memory **and** every cached store without their rows
colliding:

```sql
CREATE VIRTUAL TABLE memory_search USING fts5(
    file, section_id, title, headings, content, tags,
    store UNINDEXED,                 -- last column; see note below
    tokenize='porter unicode61'
);
CREATE TABLE memory_sections (
    store TEXT NOT NULL, file TEXT NOT NULL, section_id TEXT NOT NULL,
    /* heading, heading_level, byte_start, byte_end, content_hash ... */
    PRIMARY KEY (store, file, section_id)
);
CREATE TABLE memory_docs (
    store TEXT NOT NULL, file TEXT NOT NULL, /* category, freshness, ... */
    PRIMARY KEY (store, file)
);
```

- **`store` is `UNINDEXED` and last in the FTS5 column list.** UNINDEXED keeps
  it out of the tokenizer, so a store name can never match a `MATCH` term and
  pollute relevance — yet it is still stored and filterable with `store = ?`.
  Putting it *last* leaves every prior column's positional index unchanged, so
  `snippet(memory_search, 4, …)` still points at `content`.
- **`LocalStore = "local"`** labels the consuming repo's own content (the rows
  that existed before federation). Cached stores use their manifest name; the
  name `local` is reserved (rejected by manifest validation).

### Opt-in invariant: legacy queries stay local

Every pre-federation query method (`Search`, `GetSection`, `ListSections`,
`GetFile`, `ListFiles`) is scoped to `store = LocalStore`. So with no cached
stores the index behaves exactly as before, and even *with* cached stores the
fetch/status paths see only local rows until PR5 opts in. Multi-store retrieval
is the new `SearchPerStore(query, kPerStore, stores)`:

- Runs the FTS query once per named store with its own `LIMIT kPerStore`
  (**per-store-fair**: one noisy store can't crowd out the others in a global
  top-N), tagging each hit with its `Store`.
- The caller (PR5 fetch) merges and re-ranks across stores, applying each
  store's priority multiplier.

The incremental upsert/delete path only ever touches the local store, so
`SectionDoc.Store`/`FileDoc.Store` default to `LocalStore` when empty and the
`Delete*` helpers are hard-scoped to local. `RebuildAll` is the only writer that
sets a non-local store: it indexes the local tree (skipping `meta/cache/`) then
walks `meta/cache/stores/<name>/`, indexing each cached store under its name
(read-only — never `AssignMissingIDs`, and using the cached store's own
`meta/schema.yaml` when present).

### Migration: rebuild-on-version-bump

The schema change is a **`SchemaVersion` bump (1 → 2)**, not an in-place
migration: FTS5's column set and the new composite primary keys can't be
`ALTER`ed. Because the index is a derived, rebuildable cache, `Init` detects a
stale `PRAGMA user_version`, **drops the three tables, and recreates the current
schema empty**; the caller repopulates. Every index-opening path already
self-heals an empty index by calling `RebuildAll` (the read paths did this for
first use; PR4 added the same guard to the `propose`/`apply` write paths so a
migrated index never ends up partially populated). A fresh database
(`user_version` 0) and an already-current one both skip the drop.

## Driver

`modernc.org/sqlite` — pure-Go port. CGo-free, which keeps cross-compilation simple. FTS5 is compiled in by default. Driver name is `"sqlite"` (singular, not `"sqlite3"`).

Connection details:

```go
uri := fmt.Sprintf(
    "%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
    path,
)
db, err := sql.Open("sqlite", uri)
if err != nil { /* ... */ }
db.SetMaxOpenConns(1)
```

**Two PRAGMAs are required, not optional**, applied via the URI so they re-apply on any new connection database/sql opens:

| PRAGMA | Why |
|---|---|
| `journal_mode=WAL` | Without this, every COMMIT does a full database fsync. On Windows NTFS that costs ~12ms per commit — blows the <10ms incremental-update budget on the first try. WAL writes to a separate log file; commits only fsync the WAL. |
| `synchronous=NORMAL` | Safe with WAL. Skips fsync on every commit (relies on periodic WAL fsync). On crash the last few transactions can be lost but the database stays consistent. Acceptable because the shadow index is **derived, rebuildable** — any loss is recovered by the next `propose_update` or by `rebuild-index`. |

This combination is the standard SQLite production setting. Spike S4 confirmed the unmodified default takes ~12ms per commit on Windows; with WAL+NORMAL the per-update cost drops by an order of magnitude.

`SetMaxOpenConns(1)` matches the v0.4.1 §11 single-writer concurrency model. The advisory file lock (§11) protects across processes; within a process, the single connection serialises writes. Read paths (`fetch_context`, `status`) share the same `*sql.DB` — for read-only queries there's no contention beyond SQLite's internal mutex. WAL also enables concurrent reader/writer access without lock conflicts at the SQLite layer, useful if the future read path adopts a separate connection pool.

## Rebuild

A full rebuild walks `.agent-memory/`, parses every Markdown file, and calls `InsertSection` for every section. This is what `agent-memory rebuild-index` does. Expected cost on a 1000-section corpus: low single-digit seconds (see [s4-results.md](../spikes/s4-results.md) baselines).

Triggers:

- `init` time (empty DB; effectively a no-op).
- `rebuild-index` CLI (manual).
- `doctor` if it detects index corruption or schema mismatch.
- Never on the agent-facing hot path — only changed sections are touched there.

## API sketch (target: `internal/index/`)

```go
type Index interface {
    Open(path string) error
    Close() error
    Init(ctx context.Context) error
    UpsertSections(ctx context.Context, file string, sections []Section) error
    DeleteSections(ctx context.Context, file string, sectionIDs []string) error
    DeleteFile(ctx context.Context, file string) error
    Search(ctx context.Context, q Query) ([]Result, error)
    RebuildAll(ctx context.Context, root string) error
}
```

The wrapper handles: transaction batching, retry on `SQLITE_BUSY` (rare with single connection), and schema-version migration (via `PRAGMA user_version`).

## Alternatives considered

### Vector search (embeddings + ANN)

Out of scope for v0.x per design doc §4 Non-Goals. BM25 over curated short-form content is usually competitive with embeddings for our use case (matched conventional vocabulary, small corpus). Revisited only if benchmarks (§29) show clear regression.

### LevelDB / BoltDB / pebble

Key-value stores with secondary index. **Rejected**: we need full-text search and BM25 ranking out of the box. Building those on top of a KV store recreates SQLite FTS5.

### In-memory inverted index

Build the index in Go from scratch on each process start. **Rejected**: rebuild cost grows linearly with corpus; SQLite gives durable cache for free. For tiny corpora it's tempting, but the v0.4.1 design caters to projects with 100s of sections where startup cost matters.

### CGo-bound `mattn/go-sqlite3`

Faster than pure-Go, but adds a C toolchain dependency. **Rejected** for the design's "single Go binary, no CGo" goal. Revisited only if profiling shows pure-Go is the bottleneck.

## Performance budget

Per design doc §20.3 and implementation plan §3 S4:

- Per-section incremental update: **< 10ms** on a 1000-section index.
- Full rebuild on a 1000-section corpus: bounded only by parse + insert cost (< 5s expected).
- Search query: < 50ms on a 1000-section index (no explicit budget; spike will report).

Spike S4 measures all three; results in [s4-results.md](../spikes/s4-results.md).

## References

- [Design Doc v0.4.1 §17, §20](../../agent-memory-design-doc-v0.4.1.md) — shadow index spec.
- [Spike S4 Results](../spikes/s4-results.md) — empirical validation.
- [Implementation Plan §3 S4](../../agent-memory-implementation-plan.md).
- [SQLite FTS5 docs](https://www.sqlite.org/fts5.html).
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite).
