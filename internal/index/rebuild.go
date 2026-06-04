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

// RebuildAll wipes the index and rebuilds it from disk: first the local store
// (the consuming repo's own .agent-memory/, rooted at memDir), then every cached
// external "landscape" store under meta/cache/stores/<name>/ (federation, PR4).
// memDir is the absolute path to .agent-memory/.
//
// This is the canonical "make everything consistent" operation; the incremental
// path (per-file upserts after propose_update) keeps the local store fresh
// between rebuilds, and sync rebuilds the cached stores. With no cached stores
// the cache dir is absent and only the local store is indexed — identical to
// pre-federation behavior (the opt-in invariant).
func (i *Index) RebuildAll(ctx context.Context, memDir string, sch *schema.Schema, opts RebuildOpts) error {
	if err := i.wipeAll(ctx); err != nil {
		return fmt.Errorf("RebuildAll: wipe: %w", err)
	}
	if err := i.indexTree(ctx, LocalStore, memDir, sch, opts); err != nil {
		return fmt.Errorf("RebuildAll: local: %w", err)
	}
	if err := i.indexCachedStores(ctx, memDir, sch); err != nil {
		return fmt.Errorf("RebuildAll: cached stores: %w", err)
	}
	return nil
}

// indexTree walks baseDir and indexes every Markdown file that maps to a schema
// category, tagging each row with the given store name. The rebuildable cache
// (meta/cache relative to baseDir) is never descended into — those files belong
// to external stores and are indexed separately under their own names.
func (i *Index) indexTree(ctx context.Context, store, baseDir string, sch *schema.Schema, opts RebuildOpts) error {
	return filepath.WalkDir(baseDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(baseDir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			if relSlash == "meta/cache" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		cat, ok := sch.CategoryForPath(relSlash)
		if !ok {
			// Markdown file outside any schema category — skip silently.
			return nil
		}
		// External stores contribute durable "landscape" memory only. Skip the
		// transient local/session categories (GitTracked=false): in a git store
		// these are usually gitignored anyway, but a local-path store could
		// otherwise pull a developer's private working context into the shared
		// index, and they are retrieval noise across repos regardless.
		if store != LocalStore && !cat.GitTracked {
			return nil
		}
		return i.IndexFile(ctx, store, baseDir, relSlash, cat, opts)
	})
}

// indexCachedStores indexes each materialised external store under
// meta/cache/stores/<name>/, tagging its rows with the store name. A cached
// store is a read-only copy (synced by PR3), so AssignMissingIDs is never
// applied — mutating it would be pointless (the next sync overwrites it). Each
// store is indexed with its own meta/schema.yaml when present, falling back to
// the consuming repo's schema, so a landscape store with custom categories
// still indexes correctly. An absent cache dir is a no-op (opt-in invariant).
func (i *Index) indexCachedStores(ctx context.Context, memDir string, fallback *schema.Schema) error {
	cacheRoot := filepath.Join(memDir, "meta", "cache", "stores")
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read cache: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Transient swap artifacts left by fs.SwapDir — never index.
		if strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, ".old") {
			continue
		}
		storeDir := filepath.Join(cacheRoot, name)
		sch := fallback
		// Only an ABSENT schema falls back to the consumer's. A present-but-
		// invalid meta/schema.yaml is a configuration error and MUST fail the
		// rebuild — silently indexing the store under the consumer schema would
		// turn a broken store into a hard-to-spot retrieval bug.
		schemaPath := filepath.Join(storeDir, "meta", "schema.yaml")
		if agentfs.PathExists(schemaPath) {
			s, serr := schema.LoadSchema(schemaPath)
			if serr != nil {
				return fmt.Errorf("store %q: invalid meta/schema.yaml: %w", name, serr)
			}
			sch = s
		}
		if err := i.indexTree(ctx, name, storeDir, sch, RebuildOpts{}); err != nil {
			return fmt.Errorf("store %q: %w", name, err)
		}
	}
	return nil
}

// IndexFile reads, optionally assigns missing anchor IDs, parses, and indexes a
// single file under the given store. The full path on disk is baseDir/relPath
// (relPath in forward-slash form).
//
// AssignMissingIDs runs only for the local store AND only when the category
// requires section IDs: cached external stores are read-only copies we never
// mutate. When it runs, modified bytes are written back atomically before
// parsing; files that don't need assignments are not touched.
func (i *Index) IndexFile(ctx context.Context, store, baseDir, relPath string, cat schema.Category, opts RebuildOpts) error {
	full := filepath.Join(baseDir, filepath.FromSlash(relPath))

	src, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("IndexFile: read %s: %w", relPath, err)
	}

	if opts.AssignMissingIDs && store == LocalStore && cat.SectionIDRequired {
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
			Store:        store,
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
		Store:        store,
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
