package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/config"
	agentgit "github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/index"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// fetchOptions bundles the runtime flags. Mirrored to memory.FetchRequest
// after dependency resolution.
type fetchOptions struct {
	Root           string
	Query          string
	Budget         int
	Scope          []string
	Include        []string
	ExcludeArchive bool
}

// NewFetchCmd returns the `agent-memory fetch` subcommand.
func NewFetchCmd() *cobra.Command {
	var (
		opts   fetchOptions
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "fetch [QUERY]",
		Short: "Return a budgeted context pack from .agent-memory/",
		Long: `Search the FTS5 shadow index, apply ranking signals (scope
boost, archive penalty, stale penalty), and assemble a Markdown context
pack to stdout.

An empty QUERY returns the bootstrap pack: branch-local current state,
shared current state, conventions.md, and a compact index.md summary.

With --json, emits the full FetchResponse (context + included files +
omitted files + metadata) for programmatic consumers.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.Query = args[0]
			}
			resp, err := runFetch(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), resp.Context)
			return err
		},
	}
	cmd.Flags().StringVar(&opts.Root, "root", "", "repo root (default: current working directory)")
	cmd.Flags().IntVar(&opts.Budget, "budget", 0, "approximate character budget for the returned pack (0 = manifest default)")
	cmd.Flags().StringSliceVar(&opts.Scope, "scope", nil, "paths or module names to prioritise (repeatable, comma-separated)")
	cmd.Flags().StringSliceVar(&opts.Include, "include", nil, "category names to include (advisory in M2)")
	cmd.Flags().BoolVar(&opts.ExcludeArchive, "exclude-archive", true, "if true, archive/ files only returned on strong relevance match")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full FetchResponse as JSON instead of just the Markdown pack")
	return cmd
}

// runFetch resolves dependencies (manifest, schema, branch, index) and
// invokes the memory package to build the response. Exposed for direct
// test calls and for reuse by the MCP handler.
func runFetch(ctx context.Context, opts fetchOptions) (*memory.FetchResponse, error) {
	root, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}
	memDir := memoryDir(root)
	if ok, _ := pathExists(memDir); !ok {
		return nil, fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init` first)", memDir)
	}

	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("fetch: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("fetch: load schema: %w", err)
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("fetch: open index: %w", err)
	}
	defer idx.Close()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("fetch: init index: %w", err)
	}

	// Auto-build the index on first use. Cheap on small repos; M3 will
	// keep it warm via per-write incremental updates from propose_update.
	if n, err := idx.CountSections(ctx); err == nil && n == 0 {
		if err := idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{AssignMissingIDs: true}); err != nil {
			return nil, fmt.Errorf("fetch: rebuild index: %w", err)
		}
	}

	branch, err := agentgit.ActiveBranch(root)
	if err != nil {
		// Non-fatal: fall through with IsGitRepo=false. The fetch path
		// uses local/current.shared.md as the bootstrap local state in
		// that case.
		branch = agentgit.BranchInfo{}
	}

	// Best-effort: changed files feed the changed-file-reference ranking
	// signal. A failure (no git, not a repo) just means no boost.
	changed, _ := agentgit.ChangedFiles(root)

	return memory.BuildContextPack(ctx, memory.FetchRequest{
		Query:          opts.Query,
		Scope:          opts.Scope,
		Budget:         opts.Budget,
		Include:        opts.Include,
		ExcludeArchive: opts.ExcludeArchive,
	}, memory.FetchDeps{
		Idx:          idx,
		Schema:       sch,
		Manifest:     manifest,
		MemoryDir:    memDir,
		Branch:       branch,
		ChangedFiles: changed,
		Logger:       cliLogger(),
	})
}
