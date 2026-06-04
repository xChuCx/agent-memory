package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/memory"
)

// NewSyncCmd returns the `agent-memory sync` command: fetch & pin the
// referenced landscape stores (federation; see docs/design/federated-memory.md
// and docs/patterns/federation-stores.md).
func NewSyncCmd() *cobra.Command {
	var (
		rootFlag string
		update   bool
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch & pin referenced landscape stores into the local cache",
		Long: `Materialise every store declared in the manifest's 'stores' block into the
rebuildable cache (.agent-memory/meta/cache/stores/<name>/) and pin each to a
resolved commit in meta/stores.lock.

Per store: clone (or copy a local path) into a temp dir, reject symlinks and
contain paths, secret/PII-scan the content, then atomically swap it into the
cache. A store that fails (bad source, scan finding, ...) is reported and
skipped; the others still sync. Stores removed from the manifest are reconciled
out of the lock and cache.

Local-path sources that are not git work trees are recorded as 'unlocked' (not
reproducible). Nothing is written to the agent's context here; multi-store
fetch lands in a later release.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(rootFlag)
			if err != nil {
				return err
			}
			memDir := memoryDir(root)
			if ok, _ := pathExists(memDir); !ok {
				return fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init` first)", memDir)
			}
			m, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// Always call Sync (even with no stores) so it can reconcile a
			// lock/cache left over from removed stores.
			results, err := memory.Sync(cmd.Context(), memory.SyncDeps{
				MemoryDir: memDir,
				Manifest:  m,
				Logger:    cliLogger(),
				Update:    update,
			})
			if err != nil {
				return err
			}
			if len(results) == 0 {
				fmt.Fprintln(out, "No referenced stores declared; nothing to sync.")
				return nil
			}

			failed := 0
			for _, r := range results {
				if r.Err != nil {
					failed++
					fmt.Fprintf(out, "  FAIL  %s: %v\n", r.Name, r.Err)
					continue
				}
				state := r.ResolvedCommit
				if len(state) > 12 {
					state = state[:12]
				}
				if r.Unlocked {
					state = "unlocked (local path)"
				}
				fmt.Fprintf(out, "  ok    %s -> %s\n", r.Name, state)
			}
			fmt.Fprintf(out, "Synced %d/%d store(s).\n", len(results)-failed, len(results))
			if failed > 0 {
				return fmt.Errorf("%d store(s) failed to sync", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&update, "update", false, "move each git store's pin forward to the latest of its requested revision")
	return cmd
}
