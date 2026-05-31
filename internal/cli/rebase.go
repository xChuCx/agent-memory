package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/index"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// NewRebaseCmd returns the `agent-memory rebase` subcommand.
func NewRebaseCmd() *cobra.Command {
	var (
		rootFlag string
		force    bool
		asJSON   bool
		latest   bool
	)
	cmd := &cobra.Command{
		Use:   "rebase [STAGING_ID]",
		Short: "Re-plan a staged proposal against the current disk state",
		Long: `When a staged proposal's target sections drift (someone edited the
target .md file between stage and apply), apply <id> rejects with
target_drift. rebase tries to recover: it re-runs each operation's
Plan against the now-current bytes and rewrites the staged files +
target hashes.

Two kinds of drift:

  - HARD: the target file or section is gone entirely. Rebase
    cannot recover; reject the proposal or re-stage from scratch.
  - SOFT: the section still resolves by ID, only its content
    hash differs. Rebase can re-plan, but doing so means accepting
    the new base content as the planning input. Requires --force.

Without --force, rebase prints a diagnostic (which targets drifted,
which are soft vs hard) but changes nothing. With --force, soft
drifts are accepted and the staging directory is updated to reflect
the new base; subsequent apply succeeds.

Rebase NEVER:
  - applies to disk (only stage-area changes; user still runs apply).
  - resets the staged_at timestamp (TTL clock keeps ticking).
  - touches the rejection audit log (rebase is recovery, not discard).

STAGING_ID may be a full id or any unique prefix (Git-style). Pass
--latest instead to rebase the most recently staged proposal.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveStaging(rootFlag, args, latest)
			if err != nil {
				return err
			}
			res, err := runRebase(cmd.Context(), rootFlag, id, force)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			if err := writeRebaseHuman(cmd.OutOrStdout(), res, force); err != nil {
				return err
			}
			// Non-zero exit on rejection so scripts can fail fast.
			if res.Status == memory.StatusRejected {
				return fmt.Errorf("rebase rejected: %s", res.Reason)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&force, "force", false, "accept the new base content as planning input for soft drifts")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	cmd.Flags().BoolVar(&latest, "latest", false, "rebase the most recently staged proposal")
	return cmd
}

// runRebase loads deps and calls memory.RebaseStaged. Symmetric with
// runApply / runSweep.
func runRebase(ctx context.Context, rootFlag, stagingID string, force bool) (*memory.RebaseResult, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return nil, err
	}
	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("rebase: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("rebase: load schema: %w", err)
	}
	// Index isn't strictly needed by rebase (no re-index step), but we
	// pass an open handle for consistency with the other apply-path
	// commands — keeps UpdateDeps shape uniform.
	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("rebase: open index: %w", err)
	}
	defer func() { _ = idx.Close() }()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("rebase: init index: %w", err)
	}

	return memory.RebaseStaged(ctx, stagingID, memory.UpdateDeps{
		Manifest:  manifest,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
		Logger:    cliLogger(),
	}, force)
}

func writeRebaseHuman(w io.Writer, res *memory.RebaseResult, forceWasSet bool) error {
	switch res.Status {
	case memory.StatusSkippedClean:
		fmt.Fprintf(w, "Rebase %s: no drift detected; nothing to do.\n", res.StagingID)
	case memory.StatusRebased:
		fmt.Fprintf(w, "Rebased %s (%d file(s)):\n", res.StagingID, len(res.Files))
		for _, f := range res.Files {
			fmt.Fprintf(w, "  re-spliced: %s\n", f)
		}
		fmt.Fprintln(w, "Run `agent-memory apply` to land the proposal.")
	default: // rejected
		fmt.Fprintf(w, "Rebase REJECTED for %s\n", res.StagingID)
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
		for _, f := range res.Findings {
			fmt.Fprintf(w, "  secret:  %s at %s\n", f.Type, f.ApproximateLocation)
		}
		if res.Reason == memory.ReasonForceRequired && !forceWasSet {
			fmt.Fprintln(w, "\nRe-run with --force to accept the new base content as planning input.")
		}
	}
	return nil
}
