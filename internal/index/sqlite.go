// Package index implements the SQLite FTS5 shadow index that
// memory.fetch_context searches and propose_update updates incrementally.
//
// Production version of spike S4 (spikes/s4-fts5-incremental). The two
// PRAGMAs identified there (journal_mode=WAL, synchronous=NORMAL) are
// non-optional: without them per-COMMIT fsync dominates and per-update
// cost blows past the <10ms budget.
//
// See docs/patterns/sqlite-fts5-shadow-index.md and design doc v0.4.1 §20.
package index

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // CGo-free pure-Go driver; FTS5 enabled by default.
)

// Schema mirrors design doc v0.4.1 §20.2, extended with the federation `store`
// dimension (docs/design/federated-memory.md, PR4). Three tables:
//
//   memory_search   — FTS5 virtual table; the search hot path.
//   memory_sections — byte offsets + content hash per (store, file, section_id).
//                     The fetch path reads this to splice section bytes out
//                     of the source Markdown file.
//   memory_docs     — per-file metadata for ranking signals (freshness,
//                     archived, local_state, etc.).
//
// `store` names which memory the row came from: LocalStore for the consuming
// repo's own .agent-memory/, or a manifest store name for a cached external
// "landscape" store (meta/cache/stores/<name>/). It is UNINDEXED in the FTS5
// table — stored and filterable with `store = ?`, but never tokenized, so a
// store name can't pollute MATCH relevance. It is the LAST FTS5 column so the
// existing positional snippet() index (content = column 4) is unchanged.
const Schema = `
CREATE VIRTUAL TABLE IF NOT EXISTS memory_search USING fts5(
    file,
    section_id,
    title,
    headings,
    content,
    tags,
    store UNINDEXED,
    tokenize='porter unicode61'
);

CREATE TABLE IF NOT EXISTS memory_sections (
    store         TEXT    NOT NULL,
    file          TEXT    NOT NULL,
    section_id    TEXT    NOT NULL,
    heading       TEXT    NOT NULL,
    heading_level INTEGER NOT NULL,
    byte_start    INTEGER NOT NULL,
    byte_end      INTEGER NOT NULL,
    content_hash  TEXT    NOT NULL,
    PRIMARY KEY (store, file, section_id)
);

CREATE TABLE IF NOT EXISTS memory_docs (
    store          TEXT NOT NULL,
    file           TEXT NOT NULL,
    category       TEXT NOT NULL,
    freshness      TEXT,
    confidence     TEXT,
    last_modified  TEXT,
    committed      INTEGER DEFAULT 1,
    local_state    INTEGER DEFAULT 0,
    archived       INTEGER DEFAULT 0,
    size_bytes     INTEGER,
    checksum       TEXT,
    PRIMARY KEY (store, file)
);
`

// LocalStore is the store name for the consuming repo's own .agent-memory/
// content — every row that existed before federation. All the pre-PR4 query
// methods scope to this store, so an index with no cached external stores
// behaves exactly as it did before (the federation opt-in invariant). Cached
// external stores are indexed under their manifest name. Kept in sync with the
// reserved name rejected by config.validateStores.
const LocalStore = "local"

// SchemaVersion is the value Init stores in PRAGMA user_version. Bumping it
// triggers a rebuild-on-version-bump: Init drops the old tables and recreates
// the current schema (the index is a rebuildable cache, and the FTS5 column set
// plus the composite primary keys can't be migrated in place). The caller then
// repopulates via RebuildAll — every index-opening path guards on an empty
// index, so a migrated index self-heals on first use.
//
// v1 → v2 (PR4): added the `store` dimension to all three tables.
const SchemaVersion = 2

// Index is an open SQLite shadow index. Not safe for concurrent calls from
// multiple goroutines (the underlying *sql.DB is configured with
// SetMaxOpenConns(1) to match the single-writer concurrency model in v0.4.1
// §11).
type Index struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) the index at path. WAL + synchronous=NORMAL
// are applied via the connection URI so every connection database/sql
// hands out has them set.
func Open(path string) (*Index, error) {
	uri := fmt.Sprintf(
		"%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("index: open %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return &Index{db: db, path: path}, nil
}

// Close closes the underlying database. Idempotent — calling on a
// half-closed Index returns nil.
func (i *Index) Close() error {
	if i == nil || i.db == nil {
		return nil
	}
	err := i.db.Close()
	i.db = nil
	return err
}

// Path returns the on-disk path of the index file.
func (i *Index) Path() string { return i.path }

// Init applies the schema (idempotent via IF NOT EXISTS) and writes the
// SchemaVersion to PRAGMA user_version.
//
// Rebuild-on-version-bump: if the on-disk index was written by an older schema
// version, Init drops the stale tables before recreating them, leaving an empty
// index for the caller to repopulate via RebuildAll. We never ALTER in place —
// the FTS5 column set and the composite primary keys changed in v2, neither of
// which SQLite can migrate incrementally. A fresh database (user_version 0) and
// an already-current one are both handled by the plain CREATE IF NOT EXISTS.
func (i *Index) Init(ctx context.Context) error {
	v, err := i.Version(ctx)
	if err != nil {
		return fmt.Errorf("Init: read version: %w", err)
	}
	if v != 0 && v != SchemaVersion {
		if err := i.dropAll(ctx); err != nil {
			return fmt.Errorf("Init: drop stale schema (v%d → v%d): %w", v, SchemaVersion, err)
		}
	}
	if _, err := i.db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("Init: apply schema: %w", err)
	}
	if _, err := i.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return fmt.Errorf("Init: set user_version: %w", err)
	}
	return nil
}

// dropAll removes the three index tables. Used by Init's rebuild-on-version-bump
// path before recreating the current schema. DROP IF EXISTS so it is safe on a
// partially-initialised database.
func (i *Index) dropAll(ctx context.Context) error {
	for _, q := range []string{
		`DROP TABLE IF EXISTS memory_search`,
		`DROP TABLE IF EXISTS memory_sections`,
		`DROP TABLE IF EXISTS memory_docs`,
	} {
		if _, err := i.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// Version returns the stored PRAGMA user_version. Used by doctor and by
// the future M3+ migration check.
func (i *Index) Version(ctx context.Context) (int, error) {
	var v int
	err := i.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("Version: %w", err)
	}
	return v, nil
}
