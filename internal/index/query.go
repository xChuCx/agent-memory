package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// SearchResult is one hit from a Search query, ranked by BM25.
type SearchResult struct {
	File      string
	SectionID string
	Title     string
	Headings  string
	Snippet   string  // FTS5 snippet() output, marked with [..]
	Score     float64 // bm25() — lower is better in FTS5
	Content   string  // full indexed section body; feeds content-based ranking signals
}

// SectionInfo mirrors a memory_sections row. Returned by GetSection /
// ListSections; used by the fetch path to splice section bytes out of
// the source Markdown file.
type SectionInfo struct {
	File         string
	SectionID    string
	Heading      string
	HeadingLevel int
	ByteStart    int
	ByteEnd      int
	ContentHash  string
}

// ErrNotFound is returned when GetSection / GetFile can't find the row.
var ErrNotFound = errors.New("index: row not found")

// Search runs an FTS5 MATCH query and returns hits sorted by ascending
// BM25 score (FTS5 convention: lower = better). limit ≤ 0 falls back to
// 50.
//
// The raw query is treated as natural language, NOT as FTS5 query syntax:
// sanitizeFTSMatch tokenizes it and quotes each term, so punctuation,
// hyphens (`auto-apply`), and reserved words (AND/OR/NEAR) are matched
// literally instead of crashing the parser with a syntax/"no such column"
// error. Terms are OR-ed (match any) and ranked by BM25, so a multi-word
// natural-language query retrieves the best partial matches. A query with
// no alphanumeric tokens returns no results without error — callers (the
// fetch pipeline) handle the empty case by returning the bootstrap pack.
func (i *Index) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	match := sanitizeFTSMatch(query)
	if match == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := i.db.QueryContext(ctx,
		`SELECT
		   file,
		   section_id,
		   title,
		   headings,
		   snippet(memory_search, 4, '[', ']', '...', 16) AS snip,
		   bm25(memory_search) AS score,
		   content
		 FROM memory_search
		 WHERE memory_search MATCH ?
		 ORDER BY score
		 LIMIT ?`,
		match, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("Search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.File, &r.SectionID, &r.Title, &r.Headings, &r.Snippet, &r.Score, &r.Content); err != nil {
			return nil, fmt.Errorf("Search: scan: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// sanitizeFTSMatch turns an arbitrary natural-language query into a safe
// FTS5 MATCH expression. It extracts maximal letter/digit runs as terms and
// wraps each in double quotes, so every term is a literal phrase: FTS5
// operators (AND/OR/NOT/NEAR), column filters (`x:y`), prefixes (`*`),
// hyphens, and quotes can't reach the parser. Returns "" when the query has
// no alphanumeric content (caller treats this like an empty query).
//
// Terms are joined with OR, not AND: a natural-language query ("how does
// token refresh work") should retrieve the best PARTIAL matches, not require
// every word in one section. BM25 then ranks — a section matching more (and
// rarer) terms scores higher — and the budget greedily keeps the top of that
// ranking. Implicit-AND made multi-word queries match almost nothing, which
// dogfooding flagged as the main recall problem.
//
// Terms contain only [\p{L}\p{N}] by construction, so there are no embedded
// double quotes to escape.
func sanitizeFTSMatch(query string) string {
	var (
		b     strings.Builder
		term  strings.Builder
		first = true
	)
	flush := func() {
		if term.Len() == 0 {
			return
		}
		if !first {
			b.WriteString(" OR ")
		}
		b.WriteByte('"')
		b.WriteString(term.String())
		b.WriteByte('"')
		term.Reset()
		first = false
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			term.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return b.String()
}

// GetSection returns the memory_sections row for a (file, section_id) pair.
// Returns ErrNotFound if the row doesn't exist.
func (i *Index) GetSection(ctx context.Context, file, sectionID string) (SectionInfo, error) {
	var s SectionInfo
	err := i.db.QueryRowContext(ctx,
		`SELECT file, section_id, heading, heading_level, byte_start, byte_end, content_hash
		 FROM memory_sections WHERE file = ? AND section_id = ?`,
		file, sectionID,
	).Scan(&s.File, &s.SectionID, &s.Heading, &s.HeadingLevel, &s.ByteStart, &s.ByteEnd, &s.ContentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return SectionInfo{}, ErrNotFound
	}
	if err != nil {
		return SectionInfo{}, fmt.Errorf("GetSection: %w", err)
	}
	return s, nil
}

// ListSections returns every section for a file in document order
// (ascending byte_start).
func (i *Index) ListSections(ctx context.Context, file string) ([]SectionInfo, error) {
	rows, err := i.db.QueryContext(ctx,
		`SELECT file, section_id, heading, heading_level, byte_start, byte_end, content_hash
		 FROM memory_sections WHERE file = ?
		 ORDER BY byte_start`,
		file,
	)
	if err != nil {
		return nil, fmt.Errorf("ListSections: %w", err)
	}
	defer rows.Close()

	var out []SectionInfo
	for rows.Next() {
		var s SectionInfo
		if err := rows.Scan(&s.File, &s.SectionID, &s.Heading, &s.HeadingLevel, &s.ByteStart, &s.ByteEnd, &s.ContentHash); err != nil {
			return nil, fmt.Errorf("ListSections: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetFile returns the memory_docs row for file. Returns ErrNotFound when
// missing.
func (i *Index) GetFile(ctx context.Context, file string) (FileDoc, error) {
	var f FileDoc
	var committed, localState, archived int
	err := i.db.QueryRowContext(ctx,
		`SELECT file, category, freshness, confidence, last_modified,
		         committed, local_state, archived, size_bytes, checksum
		   FROM memory_docs WHERE file = ?`,
		file,
	).Scan(
		&f.File, &f.Category, &f.Freshness, &f.Confidence, &f.LastModified,
		&committed, &localState, &archived, &f.SizeBytes, &f.Checksum,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FileDoc{}, ErrNotFound
	}
	if err != nil {
		return FileDoc{}, fmt.Errorf("GetFile: %w", err)
	}
	f.Committed = committed != 0
	f.LocalState = localState != 0
	f.Archived = archived != 0
	return f, nil
}

// ListFiles returns memory_docs entries; if categoryFilter is non-empty,
// only rows whose category matches are returned.
func (i *Index) ListFiles(ctx context.Context, categoryFilter string) ([]FileDoc, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if categoryFilter == "" {
		rows, err = i.db.QueryContext(ctx,
			`SELECT file, category, freshness, confidence, last_modified,
			         committed, local_state, archived, size_bytes, checksum
			   FROM memory_docs ORDER BY file`)
	} else {
		rows, err = i.db.QueryContext(ctx,
			`SELECT file, category, freshness, confidence, last_modified,
			         committed, local_state, archived, size_bytes, checksum
			   FROM memory_docs WHERE category = ? ORDER BY file`,
			categoryFilter)
	}
	if err != nil {
		return nil, fmt.Errorf("ListFiles: %w", err)
	}
	defer rows.Close()

	var out []FileDoc
	for rows.Next() {
		var f FileDoc
		var committed, localState, archived int
		if err := rows.Scan(
			&f.File, &f.Category, &f.Freshness, &f.Confidence, &f.LastModified,
			&committed, &localState, &archived, &f.SizeBytes, &f.Checksum,
		); err != nil {
			return nil, fmt.Errorf("ListFiles: scan: %w", err)
		}
		f.Committed = committed != 0
		f.LocalState = localState != 0
		f.Archived = archived != 0
		out = append(out, f)
	}
	return out, rows.Err()
}
