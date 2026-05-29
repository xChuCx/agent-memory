package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/memory"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// NewApplyCmd returns the `agent-memory apply` subcommand.
func NewApplyCmd() *cobra.Command {
	var (
		rootFlag string
		asJSON   bool
		latest   bool
	)
	cmd := &cobra.Command{
		Use:   "apply [STAGING_ID]",
		Short: "Apply a staged proposal to .agent-memory/ after drift re-check",
		Long: `Re-validates every drift target recorded at stage time against the
current disk state. If any target drifted (section content changed, file
appeared/disappeared, section disappeared), the apply is rejected and
the staging directory is left intact so the agent can re-stage.

STAGING_ID may be a full id or any unique prefix (Git-style). Pass
--latest instead to act on the most recently staged proposal.

On success: every staged file is WriteAtomic'd to its destination, the
index is updated for the touched sections, and the staging directory is
removed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveStaging(rootFlag, args, latest)
			if err != nil {
				return err
			}
			res, err := runApply(cmd.Context(), rootFlag, id)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			if err := writeApplyHuman(cmd.OutOrStdout(), res); err != nil {
				return err
			}
			if res.Status != memory.StatusApplied {
				// Non-applied result is a CLI-level error so the shell exit
				// code is non-zero. JSON consumers parse Status themselves.
				return fmt.Errorf("apply rejected: %s", res.Reason)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	cmd.Flags().BoolVar(&latest, "latest", false, "act on the most recently staged proposal")
	return cmd
}

// runApply resolves deps and calls memory.ApplyStaged. Exposed for tests.
func runApply(ctx context.Context, rootFlag, stagingID string) (*memory.ApplyResult, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return nil, err
	}

	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("apply: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("apply: load schema: %w", err)
	}
	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("apply: open index: %w", err)
	}
	defer idx.Close()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("apply: init index: %w", err)
	}

	return memory.ApplyStaged(ctx, stagingID, memory.UpdateDeps{
		Manifest:  manifest,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
	})
}

func writeApplyHuman(w io.Writer, res *memory.ApplyResult) error {
	switch res.Status {
	case memory.StatusApplied:
		fmt.Fprintf(w, "Applied staging %s\n", res.StagingID)
		for _, f := range res.Files {
			fmt.Fprintf(w, "  wrote: %s\n", f)
		}
		fmt.Fprintln(w, "Staging directory removed.")
	default:
		fmt.Fprintf(w, "Apply REJECTED for %s\n", res.StagingID)
		fmt.Fprintf(w, "  reason:  %s\n", res.Reason)
		if res.Message != "" {
			fmt.Fprintf(w, "  message: %s\n", res.Message)
		}
		for _, d := range res.Drift {
			line := fmt.Sprintf("  drift:   %s", d.Path)
			if d.SectionID != "" {
				line += fmt.Sprintf(" (section: %s)", d.SectionID)
			}
			fmt.Fprintln(w, line)
			fmt.Fprintf(w, "             policy:   %s\n", d.Policy)
			fmt.Fprintf(w, "             expected: %s\n", d.Expected)
			fmt.Fprintf(w, "             found:    %s\n", d.Found)
		}
	}
	return nil
}
