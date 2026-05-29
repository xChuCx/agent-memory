package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/lock"
	"github.com/agent-memory/agent-memory/internal/memory"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// RebuildIndexResult is the structured shape returned by
// `agent-memory rebuild-index`. JSON-friendly. The numeric counts come
// from CountSections / CountFiles after the rebuild — they describe the
// final on-disk state, NOT a delta from before.
type RebuildIndexResult struct {
	FilesIndexed    int     `json:"files_indexed"`
	SectionsIndexed int     `json:"sections_indexed"`
	DurationSeconds float64 `json:"duration_seconds"`
	Clobbered       bool    `json:"clobbered,omitempty"`
	AssignedIDs     bool    `json:"assigned_ids,omitempty"`
}

// NewRebuildIndexCmd returns the `agent-memory rebuild-index` subcommand.
func NewRebuildIndexCmd() *cobra.Command {
	var (
		rootFlag    string
		asJSON      bool
		clobber     bool
		assignIDs   bool
	)
	cmd := &cobra.Command{
		Use:   "rebuild-index",
		Short: "Recreate the FTS5 shadow index from the canonical Markdown files",
		Long: `Walks .agent-memory/, parses every Markdown file that matches a
schema category, and (re)populates the FTS5 shadow index at
meta/index.sqlite. The incremental path that propose_update + apply
take on every write usually keeps the index fresh; rebuild-index is for
the cases where that isn't enough:

  - the index file got damaged (mismatched WAL, partial write after
    crash, on-disk SQLite corruption)
  - .md files were edited outside agent-memory's pipeline
  - the schema's category globs changed and files need re-categorising
  - upgrading agent-memory introduced new indexing logic

Default behaviour:
  - --assign-ids ON   inject missing <!-- @id: ... --> anchors on
                      files in categories that require them
  - --clobber  OFF    truncate the index tables (DELETE FROM ...)
                      instead of removing the SQLite file

Use --clobber when SQLite itself is corrupted (rebuild over a damaged
file would fail). --clobber removes meta/index.sqlite, -wal, -shm and
re-opens fresh.

Holds the cross-process advisory lock for the duration of the rebuild
so a concurrent propose_update can't race the wipe.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := runRebuildIndex(cmd.Context(), rebuildIndexOptions{
				Root:      rootFlag,
				Clobber:   clobber,
				AssignIDs: assignIDs,
			})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeRebuildIndexHuman(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	cmd.Flags().BoolVar(&clobber, "clobber", false, "remove the SQLite file entirely before rebuilding (use for genuine corruption)")
	cmd.Flags().BoolVar(&assignIDs, "assign-ids", true, "inject missing <!-- @id: ... --> anchors on files whose category requires them")
	return cmd
}

type rebuildIndexOptions struct {
	Root      string
	Clobber   bool
	AssignIDs bool
}

func runRebuildIndex(ctx context.Context, opts rebuildIndexOptions) (*RebuildIndexResult, error) {
	memDir, err := reviewMemDir(opts.Root)
	if err != nil {
		return nil, err
	}

	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("rebuild-index: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("rebuild-index: load schema: %w", err)
	}

	// Acquire the advisory lock BEFORE touching the SQLite file so a
	// concurrent propose_update / apply can't write into a half-wiped
	// index.
	waitTimeout := time.Duration(manifest.Concurrency.WaitTimeoutSeconds) * time.Second
	lk, err := lock.Acquire(
		filepath.Join(memDir, "meta", "lock"),
		lock.AcquireOpts{
			WaitTimeout: waitTimeout,
			Owner: lock.Metadata{
				OwnerKind: "cli-rebuild-index",
			},
		},
	)
	if err != nil {
		if errors.Is(err, lock.ErrLockHeld) {
			return nil, fmt.Errorf("rebuild-index: another writer holds the lock; try again later")
		}
		return nil, fmt.Errorf("rebuild-index: acquire lock: %w", err)
	}
	defer func() { _ = lk.Release() }()

	// --clobber: remove the SQLite file and its WAL/SHM siblings before
	// opening. Only effective when the file is genuinely damaged; the
	// non-clobber path's DELETE FROM is enough for the common case.
	if opts.Clobber {
		dbBase := filepath.Join(memDir, "meta", "index.sqlite")
		for _, sfx := range []string{"", "-wal", "-shm", "-journal"} {
			p := dbBase + sfx
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("rebuild-index: remove %s: %w", p, err)
			}
		}
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("rebuild-index: open: %w", err)
	}
	defer func() { _ = idx.Close() }()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("rebuild-index: init schema: %w", err)
	}

	start := time.Now()
	if err := idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{
		AssignMissingIDs: opts.AssignIDs,
	}); err != nil {
		return nil, fmt.Errorf("rebuild-index: %w", err)
	}
	// Regenerate the server-managed index.md routing file alongside the
	// FTS rebuild — the @generated comment even points users at this
	// command. Best-effort: a stale index.md doesn't fail the rebuild.
	_, _ = memory.RegenerateIndex(memDir, sch)
	elapsed := time.Since(start)

	files, err := idx.CountFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("rebuild-index: count files: %w", err)
	}
	sections, err := idx.CountSections(ctx)
	if err != nil {
		return nil, fmt.Errorf("rebuild-index: count sections: %w", err)
	}

	return &RebuildIndexResult{
		FilesIndexed:    files,
		SectionsIndexed: sections,
		DurationSeconds: elapsed.Seconds(),
		Clobbered:       opts.Clobber,
		AssignedIDs:     opts.AssignIDs,
	}, nil
}

func writeRebuildIndexHuman(w io.Writer, res *RebuildIndexResult) error {
	fmt.Fprintf(w, "Index rebuilt in %.2fs\n", res.DurationSeconds)
	fmt.Fprintf(w, "  files:    %d\n", res.FilesIndexed)
	fmt.Fprintf(w, "  sections: %d\n", res.SectionsIndexed)
	if res.Clobbered {
		fmt.Fprintln(w, "  mode:     clobber (SQLite file removed and recreated)")
	}
	if res.AssignedIDs {
		fmt.Fprintln(w, "  ids:      missing anchors injected where required")
	}
	return nil
}
