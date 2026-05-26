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

// Schema mirrors design doc v0.4.1 §20.2. Three tables:
//
//   memory_search   — FTS5 virtual table; the search hot path.
//   memory_sections — byte offsets + content hash per (file, section_id).
//                     The fetch path reads this to splice section bytes out
//                     of the source Markdown file.
//   memory_docs     — per-file metadata for ranking signals (freshness,
//                     archived, local_state, etc.).
const Schema = `
CREATE VIRTUAL TABLE IF NOT EXISTS memory_search USING fts5(
    file,
    section_id,
    title,
    headings,
    content,
    tags,
    tokenize='porter unicode61'
);

CREATE TABLE IF NOT EXISTS memory_sections (
    file          TEXT    NOT NULL,
    section_id    TEXT    NOT NULL,
    heading       TEXT    NOT NULL,
    heading_level INTEGER NOT NULL,
    byte_start    INTEGER NOT NULL,
    byte_end      INTEGER NOT NULL,
    content_hash  TEXT    NOT NULL,
    PRIMARY KEY (file, section_id)
);

CREATE TABLE IF NOT EXISTS memory_docs (
    file           TEXT PRIMARY KEY,
    category       TEXT NOT NULL,
    freshness      TEXT,
    confidence     TEXT,
    last_modified  TEXT,
    committed      INTEGER DEFAULT 1,
    local_state    INTEGER DEFAULT 0,
    archived       INTEGER DEFAULT 0,
    size_bytes     INTEGER,
    checksum       TEXT
);
`

// SchemaVersion is the value Init stores in PRAGMA user_version. Bumping it
// signals a schema migration; the migrator (deferred to v0.5) compares the
// stored value against this constant on open.
const SchemaVersion = 1

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
func (i *Index) Init(ctx context.Context) error {
	if _, err := i.db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("Init: apply schema: %w", err)
	}
	if _, err := i.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return fmt.Errorf("Init: set user_version: %w", err)
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
