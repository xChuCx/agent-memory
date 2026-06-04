package cli

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/config"
)

// NewStoreCmd returns the `agent-memory store` command group: declare and
// inspect referenced "landscape" stores (federation; see
// docs/design/federated-memory.md). These edit the manifest's `stores` block.
// Fetching/using a store lands in later PRs (sync, multi-store fetch); `store`
// only manages the declaration here.
func NewStoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Manage referenced landscape stores (federation)",
		Long: `Declare and inspect shared "landscape" memory stores this repo references
(e.g. a platform/architecture-memory repo). Referenced stores are recorded in
.agent-memory/meta/manifest.yaml under 'stores' and pinned in meta/stores.lock.

  agent-memory store add --name platform --source https://github.com/acme/platform-memory --revision v2025.06
  agent-memory store list
  agent-memory store rm --name platform`,
	}
	cmd.AddCommand(newStoreAddCmd(), newStoreListCmd(), newStoreRmCmd())
	return cmd
}

// manifestPathFor resolves the manifest path for rootFlag, erroring if the
// store has not been initialised.
func manifestPathFor(rootFlag string) (string, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return "", err
	}
	memDir := memoryDir(root)
	if ok, _ := pathExists(memDir); !ok {
		return "", fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init` first)", memDir)
	}
	return filepath.Join(memDir, "meta", "manifest.yaml"), nil
}

func newStoreAddCmd() *cobra.Command {
	var (
		rootFlag string
		st       config.Store
		priority float64
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a referenced landscape store to the manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			mpath, err := manifestPathFor(rootFlag)
			if err != nil {
				return err
			}
			m, err := config.LoadManifest(mpath)
			if err != nil {
				return err
			}
			for _, existing := range m.Stores {
				if existing.Name == st.Name {
					return fmt.Errorf("store %q already declared (use `store rm` first)", st.Name)
				}
			}
			// Only set the pointer when --priority was given, so an omitted
			// flag means "use the default", not "0".
			if cmd.Flags().Changed("priority-multiplier") {
				st.PriorityMultiplier = &priority
			}
			m.Stores = append(m.Stores, st)
			if err := m.Validate(); err != nil {
				return err
			}
			if err := config.WriteManifest(mpath, m); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Added store %q (%s). It is declared but not yet synced.\n", st.Name, st.Source)
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().StringVar(&st.Name, "name", "", "store name (slug: ^[a-z0-9][a-z0-9-]*$)")
	cmd.Flags().StringVar(&st.Source, "source", "", "git URL or local path to the store repo")
	cmd.Flags().StringVar(&st.Revision, "revision", "", "branch/tag/commit to pin (default: the repo's default branch)")
	cmd.Flags().StringVar(&st.Path, "path", "", "store dir within the repo (default: .agent-memory)")
	cmd.Flags().StringVar(&st.Mode, "mode", "", "access mode (default/only: read-only)")
	cmd.Flags().Float64Var(&priority, "priority-multiplier", 0, "ranking multiplier vs local 1.0 (default 0.8; must be > 0; <1 penalizes)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("source")
	return cmd
}

func newStoreListCmd() *cobra.Command {
	var rootFlag string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List referenced landscape stores and their lock state",
		RunE: func(cmd *cobra.Command, args []string) error {
			mpath, err := manifestPathFor(rootFlag)
			if err != nil {
				return err
			}
			m, err := config.LoadManifest(mpath)
			if err != nil {
				return err
			}
			lock, err := config.LoadStoresLock(filepath.Join(filepath.Dir(mpath), config.StoresLockName))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(m.Stores) == 0 {
				fmt.Fprintln(out, "No referenced stores declared.")
				return nil
			}
			stores := append([]config.Store(nil), m.Stores...)
			sort.Slice(stores, func(i, j int) bool { return stores[i].Name < stores[j].Name })
			for _, s := range stores {
				fmt.Fprintf(out, "%s\n", s.Name)
				fmt.Fprintf(out, "  source:   %s\n", s.Source)
				fmt.Fprintf(out, "  revision: %s\n", revisionOrDefault(s.Revision))
				fmt.Fprintf(out, "  path:     %s\n", s.StorePath())
				fmt.Fprintf(out, "  priority: %.2f\n", s.Priority())
				fmt.Fprintf(out, "  lock:     %s\n", lockState(lock, s.Name))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	return cmd
}

func newStoreRmCmd() *cobra.Command {
	var (
		rootFlag string
		name     string
	)
	cmd := &cobra.Command{
		Use:   "rm",
		Short: "Remove a referenced landscape store from the manifest",
		RunE: func(cmd *cobra.Command, args []string) error {
			mpath, err := manifestPathFor(rootFlag)
			if err != nil {
				return err
			}
			m, err := config.LoadManifest(mpath)
			if err != nil {
				return err
			}
			kept := m.Stores[:0]
			found := false
			for _, s := range m.Stores {
				if s.Name == name {
					found = true
					continue
				}
				kept = append(kept, s)
			}
			if !found {
				return fmt.Errorf("no store named %q", name)
			}
			m.Stores = kept
			if err := config.WriteManifest(mpath, m); err != nil {
				return err
			}
			// Drop the committed lock entry too, so the lockfile never carries
			// a dead store. The gitignored cache is reconciled on the next sync.
			lockPath := filepath.Join(filepath.Dir(mpath), config.StoresLockName)
			if lock, lerr := config.LoadStoresLock(lockPath); lerr == nil {
				if _, present := lock.Stores[name]; present {
					delete(lock.Stores, name)
					if err := config.WriteStoresLock(lockPath, lock); err != nil {
						return err
					}
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed store %q.\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().StringVar(&name, "name", "", "name of the store to remove")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func revisionOrDefault(rev string) string {
	if rev == "" {
		return "(default branch)"
	}
	return rev
}

// lockState renders the lock status of a store for `store list` / `status`.
func lockState(lock *config.StoresLock, name string) string {
	ls, ok := lock.Stores[name]
	if !ok {
		return "not synced"
	}
	if ls.Unlocked {
		return "unlocked (local path, not reproducible)"
	}
	if ls.ResolvedCommit == "" {
		return "not synced"
	}
	short := ls.ResolvedCommit
	if len(short) > 12 {
		short = short[:12]
	}
	return short
}
