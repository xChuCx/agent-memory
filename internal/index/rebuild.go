package index

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
	agentmd "github.com/xChuCx/agent-memory/internal/markdown"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// RebuildOpts configures RebuildAll.
type RebuildOpts struct {
	// AssignMissingIDs runs internal/markdown.AssignMissingIDs on every
	// file whose category has SectionIDRequired=true before indexing.
	// Mutates the file on disk via atomic write; only modified files are
	// touched. Idempotent — running rebuild twice produces the same
	// on-disk state.
	AssignMissingIDs bool
}

// RebuildAll wipes the index and re-walks memDir, indexing every Markdown
// file that maps to a schema category. memDir is the absolute path to
// .agent-memory/.
//
// The two pre-existing rows for any file present in the wipe survive — we
// fully reset the three index tables and rebuild from disk. This is the
// canonical "make everything consistent" operation; M3's incremental path
// (per-file upserts after propose_update) keeps the index fresh between
// rebuilds.
func (i *Index) RebuildAll(ctx context.Context, memDir string, sch *schema.Schema, opts RebuildOpts) error {
	if err := i.wipeAll(ctx); err != nil {
		return fmt.Errorf("RebuildAll: wipe: %w", err)
	}

	walkErr := filepath.WalkDir(memDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}

		rel, err := filepath.Rel(memDir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)

		cat, ok := sch.CategoryForPath(relSlash)
		if !ok {
			// Markdown file outside any schema category — skip silently.
			return nil
		}

		return i.IndexFile(ctx, memDir, relSlash, cat, opts)
	})
	if walkErr != nil {
		return fmt.Errorf("RebuildAll: walk: %w", walkErr)
	}
	return nil
}

// IndexFile reads, optionally assigns missing anchor IDs, parses, and
// indexes a single file. The full path on disk is memDir/relPath (relPath
// in forward-slash form).
//
// If opts.AssignMissingIDs is true AND the category requires section IDs,
// AssignMissingIDs runs first; modified bytes are written back atomically
// before parsing. Files that don't need assignments are not touched.
func (i *Index) IndexFile(ctx context.Context, memDir, relPath string, cat schema.Category, opts RebuildOpts) error {
	full := filepath.Join(memDir, filepath.FromSlash(relPath))

	src, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("IndexFile: read %s: %w", relPath, err)
	}

	if opts.AssignMissingIDs && cat.SectionIDRequired {
		newSrc, _, err := agentmd.AssignMissingIDs(src)
		if err != nil {
			return fmt.Errorf("IndexFile: assign IDs %s: %w", relPath, err)
		}
		if !bytes.Equal(newSrc, src) {
			if err := agentfs.WriteAtomic(full, newSrc, 0644); err != nil {
				return fmt.Errorf("IndexFile: write back %s: %w", relPath, err)
			}
			src = newSrc
		}
	}

	sections, err := agentmd.ParseSections(src)
	if err != nil {
		return fmt.Errorf("IndexFile: parse %s: %w", relPath, err)
	}

	docs := make([]SectionDoc, 0, len(sections))
	for _, s := range sections {
		// Skip sections without anchor IDs if the category requires them.
		// (Should only happen when opts.AssignMissingIDs is false.)
		if cat.SectionIDRequired && s.AnchorID == "" {
			continue
		}
		body := string(src[s.ByteStart:s.ByteEnd])
		docs = append(docs, SectionDoc{
			File:         relPath,
			SectionID:    s.AnchorID,
			Heading:      s.HeadingText,
			HeadingLevel: s.HeadingLevel,
			Title:        s.HeadingText,
			Headings:     s.HeadingText, // future: ancestor breadcrumb
			Content:      body,
			Tags:         "",
			ByteStart:    s.ByteStart,
			ByteEnd:      s.ByteEnd,
			ContentHash:  s.ContentHash,
		})
	}
	if err := i.UpsertSections(ctx, docs); err != nil {
		return err
	}

	info, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("IndexFile: stat %s: %w", relPath, err)
	}
	return i.UpsertFile(ctx, FileDoc{
		File:         relPath,
		Category:     cat.Name,
		LastModified: info.ModTime().UTC().Format(time.RFC3339),
		Committed:    cat.GitTracked,
		LocalState:   !cat.GitTracked,
		Archived:     strings.HasPrefix(relPath, "archive/"),
		SizeBytes:    int(info.Size()),
		Checksum:     "", // file-level hash deferred to M3+
	})
}

// wipeAll empties the three index tables. Used by RebuildAll before the
// re-walk; not exported because callers should use the higher-level
// rebuild API rather than partial wipes.
func (i *Index) wipeAll(ctx context.Context) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM memory_search`,
		`DELETE FROM memory_sections`,
		`DELETE FROM memory_docs`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
