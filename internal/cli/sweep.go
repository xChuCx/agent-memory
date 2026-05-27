package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/memory"
)

// NewSweepCmd returns the `agent-memory sweep` subcommand.
//
// Removes every staged proposal whose age exceeds the manifest's
// `staging.ttl_seconds`. Each removal is logged to
// meta/rejection-log.jsonl with reason="ttl_expired".
//
// --ttl overrides the manifest value for a one-off aggressive cleanup
// (e.g., `agent-memory sweep --ttl 1h` before a release). --dry-run
// previews without changing anything.
func NewSweepCmd() *cobra.Command {
	var (
		rootFlag string
		ttlFlag  time.Duration
		dryRun   bool
		asJSON   bool
	)
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Remove staged proposals past the manifest TTL",
		Long: `Walks .agent-memory/staging/ and removes every proposal
older than the manifest's staging.ttl_seconds. Each removal is also
appended to meta/rejection-log.jsonl with reason="ttl_expired".

--ttl overrides the manifest value for one-off cleanups. --dry-run
lists what would be removed without changing anything. --json emits
the full SweepResult for programmatic consumers.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := runSweep(rootFlag, ttlFlag, dryRun)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeSweepHuman(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().DurationVar(&ttlFlag, "ttl", 0, "override manifest staging.ttl_seconds (e.g., 1h, 24h, 168h)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "list what would be removed; don't change anything")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	return cmd
}

func runSweep(rootFlag string, ttlOverride time.Duration, dryRun bool) (*memory.SweepResult, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return nil, err
	}
	ttl := ttlOverride
	if ttl == 0 {
		manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
		if err != nil {
			return nil, fmt.Errorf("sweep: load manifest: %w", err)
		}
		ttl = time.Duration(manifest.Staging.TTLSeconds) * time.Second
	}
	return memory.SweepStale(memDir, ttl, dryRun)
}

func writeSweepHuman(w io.Writer, res *memory.SweepResult) error {
	verb := "removed"
	if res.DryRun {
		verb = "would remove"
	}
	switch {
	case len(res.Expired) == 0:
		fmt.Fprintln(w, "No staged proposals past TTL.")
	default:
		fmt.Fprintf(w, "%s %d staged proposal(s):\n\n", capFirst(verb), len(res.Expired))
		for _, e := range res.Expired {
			fmt.Fprintf(w, "  %s\n", e.StagingID)
			if e.Intent != "" {
				fmt.Fprintf(w, "    intent:    %s\n", e.Intent)
			}
			if e.Rationale != "" {
				fmt.Fprintf(w, "    rationale: %s\n", e.Rationale)
			}
			if e.StagedAt != "" {
				fmt.Fprintf(w, "    staged:    %s\n", e.StagedAt)
			}
			fmt.Fprintf(w, "    age:       %s\n", humanizeAge(e.AgeSeconds))
			fmt.Fprintln(w)
		}
		if res.DryRun {
			fmt.Fprintln(w, "(dry-run; nothing was removed. Re-run without --dry-run to apply.)")
		}
	}
	return nil
}

// capFirst uppercases the first ASCII letter of s — used so the human
// output reads "Removed N..." even though the verb lives in a local
// switch as "removed".
func capFirst(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-32) + s[1:]
	}
	return s
}

// humanizeAge renders an age-in-seconds as the largest natural unit
// (days/hours/minutes/seconds). Good enough for an audit list.
func humanizeAge(seconds int) string {
	switch {
	case seconds >= 86400:
		return fmt.Sprintf("%dd %dh", seconds/86400, (seconds%86400)/3600)
	case seconds >= 3600:
		return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
	case seconds >= 60:
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
