// Package s4 verifies that SQLite FTS5 supports incremental per-section
// updates fast enough for our shadow index. The exit criterion (per
// implementation plan §3 S4) is "<10ms per update on a 1000-section index".
//
// FTS5 does not support ON CONFLICT, so per-section updates use a
// DELETE-then-INSERT pattern inside a single transaction. The companion
// memory_sections table (byte offsets, hashes) uses standard UPSERT.
//
// See ../../docs/spikes/s4-results.md and
// ../../docs/patterns/sqlite-fts5-shadow-index.md.
package s4

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // CGo-free SQLite driver, FTS5 enabled by default
)

// Schema mirrors design doc v0.4.1 §20.2 (with memory_docs omitted for the
// spike — only memory_search and memory_sections are needed to validate the
// incremental pattern).
const Schema = `
CREATE VIRTUAL TABLE memory_search USING fts5(
    file,
    section_id,
    title,
    headings,
    content,
    tags,
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
`

// Section is what gets indexed. The fields map 1:1 to the design doc's
// per-section metadata; the spike uses synthetic data via fixtures.go.
type Section struct {
	File         string
	SectionID    string
	Heading      string
	HeadingLevel int
	Title        string // typically equals Heading
	Headings     string // breadcrumb of ancestor headings
	Content      string // section body
	Tags         string // space-separated
	ByteStart    int
	ByteEnd      int
	ContentHash  string
}

// Open opens (or creates) a SQLite database at path using the modernc.org
// pure-Go driver. The caller is responsible for Close.
//
// Two PRAGMAs are applied via the connection URI so they are re-applied on
// any new connection that database/sql opens:
//
//   journal_mode=WAL      — write-ahead log. Commits do not require a full
//                           database fsync; only the WAL is fsync'd, and only
//                           periodically (controlled by synchronous level).
//                           Without this, on Windows each COMMIT pays ~12ms
//                           for fsync, blowing the <10ms per-update budget.
//
//   synchronous=NORMAL    — safe with WAL. Skips fsync on every commit. On
//                           crash the last few transactions can be lost but
//                           the database remains consistent. Acceptable for
//                           our shadow index (derived, rebuildable from the
//                           Markdown files; loss of recent index updates is
//                           recovered by the next propose_update).
//
// SetMaxOpenConns(1) matches the v0.4.1 §11 single-writer concurrency model.
func Open(path string) (*sql.DB, error) {
	uri := fmt.Sprintf(
		"%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// ApplySchema creates the FTS5 table and the memory_sections table.
func ApplySchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, Schema)
	return err
}

// InsertSection inserts a section into both memory_search and memory_sections.
// Fails if (file, section_id) already exists in memory_sections.
func InsertSection(ctx context.Context, db *sql.DB, s Section) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_search (file, section_id, title, headings, content, tags)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.File, s.SectionID, s.Title, s.Headings, s.Content, s.Tags,
	); err != nil {
		return fmt.Errorf("insert memory_search: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_sections
		   (file, section_id, heading, heading_level, byte_start, byte_end, content_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.File, s.SectionID, s.Heading, s.HeadingLevel,
		s.ByteStart, s.ByteEnd, s.ContentHash,
	); err != nil {
		return fmt.Errorf("insert memory_sections: %w", err)
	}

	return tx.Commit()
}

// UpsertSection performs the incremental update pattern: DELETE then INSERT
// for the FTS5 row (which has no ON CONFLICT support), plus a regular UPSERT
// for memory_sections. The whole operation runs in one transaction.
func UpsertSection(ctx context.Context, db *sql.DB, s Section) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memory_search WHERE file = ? AND section_id = ?`,
		s.File, s.SectionID,
	); err != nil {
		return fmt.Errorf("delete memory_search: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_search (file, section_id, title, headings, content, tags)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.File, s.SectionID, s.Title, s.Headings, s.Content, s.Tags,
	); err != nil {
		return fmt.Errorf("insert memory_search: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO memory_sections
		   (file, section_id, heading, heading_level, byte_start, byte_end, content_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (file, section_id) DO UPDATE SET
		   heading       = excluded.heading,
		   heading_level = excluded.heading_level,
		   byte_start    = excluded.byte_start,
		   byte_end      = excluded.byte_end,
		   content_hash  = excluded.content_hash`,
		s.File, s.SectionID, s.Heading, s.HeadingLevel,
		s.ByteStart, s.ByteEnd, s.ContentHash,
	); err != nil {
		return fmt.Errorf("upsert memory_sections: %w", err)
	}

	return tx.Commit()
}

// SearchResult is a single FTS5 MATCH hit, ranked by BM25 (lower is better).
type SearchResult struct {
	File      string
	SectionID string
	Title     string
	Headings  string
	Snippet   string  // FTS5 snippet() helper output
	Score     float64 // bm25() — lower = better
}

// Search runs an FTS5 MATCH query and returns results sorted by BM25 ascending.
func Search(ctx context.Context, db *sql.DB, query string, limit int) ([]SearchResult, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT
		   file,
		   section_id,
		   title,
		   headings,
		   snippet(memory_search, 4, '[', ']', '...', 16) AS snip,
		   bm25(memory_search) AS score
		 FROM memory_search
		 WHERE memory_search MATCH ?
		 ORDER BY score
		 LIMIT ?`,
		query, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.File, &r.SectionID, &r.Title, &r.Headings, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// CountSections returns the number of rows in memory_sections.
func CountSections(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_sections`).Scan(&n)
	return n, err
}
