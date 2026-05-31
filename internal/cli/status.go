package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/config"
	agentgit "github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// StatusReport is the structured shape returned by `agent-memory status`.
// Wire format = design doc §15.11 (the same shape memory.status MCP tool
// returns) plus a few CLI-specific helpers (Root, MemoryDir, paths to
// manifest/schema, per-category counts). The shared §15.11 block is
// embedded so JSON output flattens to one object.
type StatusReport struct {
	// §15.11 — every field the design spec lists, in the same shape.
	*memory.MemoryStatus

	// CLI-only extras: useful for human/script readers but not part of
	// the design's memory.status contract.
	Root         string         `json:"root"`
	MemoryDir    string         `json:"memory_dir"`
	ManifestPath string         `json:"manifest_path"`
	SchemaPath   string         `json:"schema_path"`
	Categories   map[string]int `json:"categories"`
}

// NewStatusCmd returns the `agent-memory status` subcommand.
func NewStatusCmd() *cobra.Command {
	var (
		rootFlag string
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Print agent-memory state for the current repo",
		Long: `Reads .agent-memory/meta/manifest.yaml and meta/schema.yaml,
walks the tree to count files per category, summarises staged proposals
(with drift detection per target), and reports lock + security + git
metadata. Output shape matches design §15.11 for the memory.status MCP
tool, plus CLI-specific path fields.

Returns an error if .agent-memory/ does not exist.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := runStatus(cmd.Context(), rootFlag)
			if err != nil {
				return err
			}
			if asJSON {
				return writeStatusJSON(cmd.OutOrStdout(), r)
			}
			return writeStatusHuman(cmd.OutOrStdout(), r)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	return cmd
}

// runStatus assembles the StatusReport. Exposed for direct test calls.
func runStatus(ctx context.Context, rootFlag string) (*StatusReport, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return nil, err
	}
	memDir := memoryDir(root)
	if ok, _ := pathExists(memDir); !ok {
		return nil, fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init` first)", memDir)
	}

	manifestPath := filepath.Join(memDir, "meta", "manifest.yaml")
	schemaPath := filepath.Join(memDir, "meta", "schema.yaml")

	m, err := config.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("status: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("status: load schema: %w", err)
	}

	// Best-effort branch resolution: non-git repo or missing git binary
	// → zero BranchInfo. BuildStatus tolerates that.
	branch, _ := agentgit.ActiveBranch(root)

	mem, err := memory.BuildStatus(ctx, memory.StatusDeps{
		MemoryDir:     memDir,
		Manifest:      m,
		Schema:        sch,
		Branch:        branch,
		MemoryVersion: ProgramVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}

	categories, err := countCategoriesUnder(memDir, sch)
	if err != nil {
		return nil, fmt.Errorf("status: count files: %w", err)
	}

	return &StatusReport{
		MemoryStatus: mem,
		Root:         root,
		MemoryDir:    memDir,
		ManifestPath: manifestPath,
		SchemaPath:   schemaPath,
		Categories:   categories,
	}, nil
}

// countCategoriesUnder walks memDir and counts .md files per schema category.
// Files that don't match any category (e.g., stray notes someone dropped in)
// are silently ignored — they don't show up in counts.
func countCategoriesUnder(memDir string, sch *schema.Schema) (map[string]int, error) {
	counts := make(map[string]int, len(sch.Categories))
	for name := range sch.Categories {
		counts[name] = 0
	}

	err := filepath.WalkDir(memDir, func(p string, d fs.DirEntry, walkErr error) error {
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
		if cat, ok := sch.CategoryForPath(relSlash); ok {
			counts[cat.Name]++
		}
		return nil
	})
	return counts, err
}

func writeStatusJSON(w io.Writer, r *StatusReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func writeStatusHuman(w io.Writer, r *StatusReport) error {
	// Top banner.
	fmt.Fprintf(w, "agent-memory %s\n", r.MemoryVersion)
	fmt.Fprintf(w, "repo: %s\n", r.Repo)
	fmt.Fprintf(w, "root: %s\n", r.Root)
	if r.ActiveBranch != "" {
		fmt.Fprintf(w, "branch: %s\n", r.ActiveBranch)
	}
	fmt.Fprintln(w)

	// File counts.
	fmt.Fprintf(w, "Files:\n")
	fmt.Fprintf(w, "  durable:        %d\n", r.DurableFiles)
	fmt.Fprintf(w, "  archive:        %d\n", r.ArchiveFiles)
	fmt.Fprintf(w, "  sessions:       %d\n", r.LocalSessions)
	fmt.Fprintf(w, "  local current:  %d\n", r.LocalCurrentFiles)
	if len(r.OrphanLocalFiles) > 0 {
		fmt.Fprintf(w, "  orphans:        %d (%s)\n", len(r.OrphanLocalFiles), strings.Join(r.OrphanLocalFiles, ", "))
	}
	fmt.Fprintln(w)

	// Sizes.
	fmt.Fprintf(w, "Sizes:\n")
	fmt.Fprintf(w, "  index:          %d bytes\n", r.IndexSizeBytes)
	fmt.Fprintf(w, "  current state:  %d bytes\n", r.CurrentSizeBytes)
	fmt.Fprintln(w)

	// Per-category breakdown (CLI extra).
	fmt.Fprintln(w, "Categories:")
	names := make([]string, 0, len(r.Categories))
	for name := range r.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-15s %d\n", name, r.Categories[name])
	}
	fmt.Fprintln(w)

	// Staged updates.
	if len(r.StagedUpdates) > 0 {
		fmt.Fprintf(w, "Staged updates (%d):\n", len(r.StagedUpdates))
		for _, s := range r.StagedUpdates {
			marker := ""
			if s.DriftDetected {
				marker = " [DRIFT]"
			}
			fmt.Fprintf(w, "  %s%s\n", s.ID, marker)
			fmt.Fprintf(w, "    intent: %s, age: %ds, ttl remaining: %ds\n",
				s.Intent, s.AgeSeconds, s.TTLRemainingSeconds)
			if len(s.TargetFiles) > 0 {
				fmt.Fprintf(w, "    files:  %s\n", strings.Join(s.TargetFiles, ", "))
			}
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, "Staged updates: none")
		fmt.Fprintln(w)
	}

	// Security / git / lock.
	fmt.Fprintf(w, "Security:  last_scan=%s, allowlisted_regions=%d, untrusted_sources=%d\n",
		r.Security.LastSecretScan, r.Security.AllowlistedRegions, r.Security.UntrustedSources)
	fmt.Fprintf(w, "Git:       track_local=%t, track_sessions=%t, ignored_local=%t, merge_driver=%t\n",
		r.Git.TrackLocal, r.Git.TrackSessions, r.Git.IgnoredLocalState, r.Git.MergeDriverInstalled)
	fmt.Fprintf(w, "Lock:      held=%t, stale_recoveries_24h=%d\n",
		r.Lock.Held, r.Lock.StaleRecoveriesLast24h)
	return nil
}
