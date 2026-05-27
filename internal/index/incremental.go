package index

import (
	"context"
	"fmt"
)

// SectionDoc bundles the per-section data the index needs. Field names mirror
// the FTS5 + memory_sections columns; the index doesn't try to validate them
// further (e.g., that ByteEnd > ByteStart) — that's the caller's job.
type SectionDoc struct {
	File         string
	SectionID    string
	Heading      string
	HeadingLevel int
	Title        string // FTS5: typically equals Heading
	Headings     string // FTS5: breadcrumb of ancestor headings (future use)
	Content      string // FTS5: section body
	Tags         string // FTS5: space-separated tag tokens (future use)
	ByteStart    int
	ByteEnd      int
	ContentHash  string
}

// FileDoc bundles per-file metadata for ranking + status reporting.
type FileDoc struct {
	File         string
	Category     string
	Freshness    string
	Confidence   string
	LastModified string // RFC3339; empty if unknown
	Committed    bool
	LocalState   bool
	Archived     bool
	SizeBytes    int
	Checksum     string
}

// UpsertSections replaces (file, section_id) rows for every entry in
// `sections`. Each row is handled in one DB transaction:
//
//   DELETE FROM memory_search WHERE file=? AND section_id=?
//   INSERT INTO memory_search (...) VALUES (...)
//   INSERT INTO memory_sections (...) VALUES (...) ON CONFLICT DO UPDATE
//
// FTS5 has no ON CONFLICT, so memory_search uses DELETE-then-INSERT.
// memory_sections uses standard UPSERT.
//
// An empty slice is a no-op (no transaction opened).
func (i *Index) UpsertSections(ctx context.Context, sections []SectionDoc) error {
	if len(sections) == 0 {
		return nil
	}
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("UpsertSections: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, s := range sections {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM memory_search WHERE file = ? AND section_id = ?`,
			s.File, s.SectionID,
		); err != nil {
			return fmt.Errorf("UpsertSections: delete search (%s/%s): %w", s.File, s.SectionID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_search (file, section_id, title, headings, content, tags)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			s.File, s.SectionID, s.Title, s.Headings, s.Content, s.Tags,
		); err != nil {
			return fmt.Errorf("UpsertSections: insert search (%s/%s): %w", s.File, s.SectionID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO memory_sections (file, section_id, heading, heading_level, byte_start, byte_end, content_hash)
			 VALUES (?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (file, section_id) DO UPDATE SET
			   heading       = excluded.heading,
			   heading_level = excluded.heading_level,
			   byte_start    = excluded.byte_start,
			   byte_end      = excluded.byte_end,
			   content_hash  = excluded.content_hash`,
			s.File, s.SectionID, s.Heading, s.HeadingLevel, s.ByteStart, s.ByteEnd, s.ContentHash,
		); err != nil {
			return fmt.Errorf("UpsertSections: upsert sections (%s/%s): %w", s.File, s.SectionID, err)
		}
	}
	return tx.Commit()
}

// DeleteSections removes the listed (file, section_id) rows from both
// memory_search and memory_sections in a single transaction. An empty
// sectionIDs slice is a no-op.
func (i *Index) DeleteSections(ctx context.Context, file string, sectionIDs []string) error {
	if len(sectionIDs) == 0 {
		return nil
	}
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("DeleteSections: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, id := range sectionIDs {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM memory_search WHERE file = ? AND section_id = ?`,
			file, id,
		); err != nil {
			return fmt.Errorf("DeleteSections: delete search (%s/%s): %w", file, id, err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM memory_sections WHERE file = ? AND section_id = ?`,
			file, id,
		); err != nil {
			return fmt.Errorf("DeleteSections: delete sections (%s/%s): %w", file, id, err)
		}
	}
	return tx.Commit()
}

// DeleteFile removes ALL rows for the given file across the three tables.
// Used when a memory file is removed from the layout (e.g., archive flow).
func (i *Index) DeleteFile(ctx context.Context, file string) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("DeleteFile: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{
		`DELETE FROM memory_search WHERE file = ?`,
		`DELETE FROM memory_sections WHERE file = ?`,
		`DELETE FROM memory_docs WHERE file = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, file); err != nil {
			return fmt.Errorf("DeleteFile (%s): %w", file, err)
		}
	}
	return tx.Commit()
}

// UpsertFile inserts or updates the memory_docs row for file.
func (i *Index) UpsertFile(ctx context.Context, doc FileDoc) error {
	_, err := i.db.ExecContext(ctx,
		`INSERT INTO memory_docs (
		   file, category, freshness, confidence, last_modified,
		   committed, local_state, archived, size_bytes, checksum
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (file) DO UPDATE SET
		   category      = excluded.category,
		   freshness     = excluded.freshness,
		   confidence    = excluded.confidence,
		   last_modified = excluded.last_modified,
		   committed     = excluded.committed,
		   local_state   = excluded.local_state,
		   archived      = excluded.archived,
		   size_bytes    = excluded.size_bytes,
		   checksum      = excluded.checksum`,
		doc.File, doc.Category, doc.Freshness, doc.Confidence, doc.LastModified,
		boolToInt(doc.Committed), boolToInt(doc.LocalState), boolToInt(doc.Archived),
		doc.SizeBytes, doc.Checksum,
	)
	if err != nil {
		return fmt.Errorf("UpsertFile (%s): %w", doc.File, err)
	}
	return nil
}

// CountSections returns the total number of rows in memory_sections.
// Used by tests and status reporting.
func (i *Index) CountSections(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_sections`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountSections: %w", err)
	}
	return n, nil
}

// CountFiles returns the total number of rows in memory_docs.
func (i *Index) CountFiles(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_docs`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("CountFiles: %w", err)
	}
	return n, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
