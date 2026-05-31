package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/memory"
)

// NewRejectCmd returns the `agent-memory reject` subcommand.
func NewRejectCmd() *cobra.Command {
	var (
		rootFlag string
		asJSON   bool
		latest   bool
	)
	cmd := &cobra.Command{
		Use:   "reject [STAGING_ID]",
		Short: "Discard a staged proposal without applying",
		Long: `Removes .agent-memory/staging/<STAGING_ID>/ from disk. No drift
checks, no lock — the proposal hasn't touched any other files. The
agent receives no notification; if it cares, it can detect the staged
dir disappearing by re-issuing fetch_context.

STAGING_ID may be a full id or any unique prefix (Git-style). Pass
--latest instead to discard the most recently staged proposal.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := resolveStaging(rootFlag, args, latest)
			if err != nil {
				return err
			}
			res, err := runReject(rootFlag, id)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			if err := writeRejectHuman(cmd.OutOrStdout(), res); err != nil {
				return err
			}
			if res.Reason == memory.ReasonStagingNotFound {
				return fmt.Errorf("reject: %s", res.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	cmd.Flags().BoolVar(&latest, "latest", false, "discard the most recently staged proposal")
	return cmd
}

// runReject is the test-friendly entry point. No deps needed beyond memDir.
func runReject(rootFlag, stagingID string) (*memory.ApplyResult, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return nil, err
	}
	return memory.RejectStaged(memDir, stagingID)
}

func writeRejectHuman(w io.Writer, res *memory.ApplyResult) error {
	if res.Reason == memory.ReasonStagingNotFound {
		fmt.Fprintf(w, "No staged proposal %q to reject.\n", res.StagingID)
		return nil
	}
	fmt.Fprintf(w, "Rejected staging %s; directory removed.\n", res.StagingID)
	return nil
}
