package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/memory"
)

// ReviewList is the structured output for `agent-memory review` with no id.
type ReviewList struct {
	Proposals []memory.StagedProposal `json:"proposals"`
}

// ReviewDetail is the structured output for `agent-memory review <id>`. Files
// is populated only when --show is set (otherwise the staged file contents
// are not echoed to keep output focused on metadata).
type ReviewDetail struct {
	Proposal *memory.StagedProposal   `json:"proposal"`
	Targets  []memory.OperationTarget `json:"targets"`
	Files    map[string]string        `json:"files,omitempty"`
}

// NewReviewCmd returns the `agent-memory review` subcommand.
func NewReviewCmd() *cobra.Command {
	var (
		rootFlag    string
		asJSON      bool
		showContent bool
		latest      bool
	)
	cmd := &cobra.Command{
		Use:   "review [STAGING_ID]",
		Short: "List staged proposals or show one in detail",
		Long: `Without arguments: prints every staged proposal under
.agent-memory/staging/ in chronological order with intent, rationale,
file count, and stage timestamp.

With a STAGING_ID (full id or unique prefix, Git-style) or --latest:
prints that proposal's full metadata, drift targets, and (with --show)
the post-state bytes of every file the proposal would write. The on-disk
current state is NOT shown — pipe the staged content into your usual
diff tool against the live file.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// No arg and no --latest → list mode.
			if len(args) == 0 && !latest {
				summary, err := runReviewList(rootFlag)
				if err != nil {
					return err
				}
				if asJSON {
					return writeJSON(cmd.OutOrStdout(), summary)
				}
				return writeReviewListHuman(cmd.OutOrStdout(), summary)
			}
			id, err := resolveStaging(rootFlag, args, latest)
			if err != nil {
				return err
			}
			detail, err := runReviewDetail(rootFlag, id, showContent)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), detail)
			}
			return writeReviewDetailHuman(cmd.OutOrStdout(), detail, showContent)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	cmd.Flags().BoolVar(&showContent, "show", false, "include the staged post-state of every file (detail mode only)")
	cmd.Flags().BoolVar(&latest, "latest", false, "show the most recently staged proposal in detail")
	return cmd
}

// runReviewList loads all staged proposals.
func runReviewList(rootFlag string) (*ReviewList, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return nil, err
	}
	props, err := memory.ListStaged(memDir)
	if err != nil {
		return nil, fmt.Errorf("review: %w", err)
	}
	return &ReviewList{Proposals: props}, nil
}

// runReviewDetail loads one proposal + its targets + (optionally) staged
// file contents.
func runReviewDetail(rootFlag, stagingID string, showContent bool) (*ReviewDetail, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return nil, err
	}
	if !memory.StagingExists(memDir, stagingID) {
		return nil, fmt.Errorf("review: no staged proposal %q under %s", stagingID, filepath.Join(memDir, "staging"))
	}
	p, err := memory.LoadStaged(memDir, stagingID)
	if err != nil {
		return nil, err
	}
	// Pin the staging id to the dir name (same invariant ListStaged uses).
	p.StagingID = stagingID

	targets, err := memory.LoadStagedTargets(memDir, stagingID)
	if err != nil {
		return nil, err
	}

	out := &ReviewDetail{Proposal: p, Targets: targets}
	if showContent {
		files := make(map[string]string, len(p.Files))
		for _, rel := range p.Files {
			abs := filepath.Join(memDir, "staging", stagingID, "files", filepath.FromSlash(rel))
			b, err := os.ReadFile(abs)
			if err != nil {
				return nil, fmt.Errorf("review: read staged %s: %w", rel, err)
			}
			files[rel] = string(b)
		}
		out.Files = files
	}
	return out, nil
}

// reviewMemDir resolves --root → memDir and confirms .agent-memory/ exists.
// Shared by review/apply/reject.
func reviewMemDir(rootFlag string) (string, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return "", err
	}
	memDir := memoryDir(root)
	if ok, _ := pathExists(memDir); !ok {
		return "", fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init` first)", memDir)
	}
	return memDir, nil
}

// resolveStaging turns CLI args + the --latest flag into a full staging
// ID via memory.ResolveStagingID. Shared by apply / reject / rebase /
// review so they all accept Git-style prefixes and --latest uniformly.
//
//   - latest=true       → newest staged proposal (positional arg ignored).
//   - exactly one arg   → exact id or unique prefix.
//   - neither           → error prompting for one.
func resolveStaging(rootFlag string, args []string, latest bool) (string, error) {
	memDir, err := reviewMemDir(rootFlag)
	if err != nil {
		return "", err
	}
	switch {
	case latest:
		return memory.ResolveStagingID(memDir, memory.LatestRef)
	case len(args) == 1:
		return memory.ResolveStagingID(memDir, args[0])
	default:
		return "", fmt.Errorf("provide a STAGING_ID (full or unique prefix) or pass --latest")
	}
}

// writeJSON encodes v as pretty-printed JSON. Shared by review/apply/reject.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeReviewListHuman(w io.Writer, list *ReviewList) error {
	if len(list.Proposals) == 0 {
		_, err := fmt.Fprintln(w, "No staged proposals.")
		return err
	}
	fmt.Fprintf(w, "%d staged proposal(s):\n\n", len(list.Proposals))
	for _, p := range list.Proposals {
		fmt.Fprintf(w, "  %s\n", p.StagingID)
		fmt.Fprintf(w, "    intent:    %s\n", p.Request.Intent)
		if p.Request.Rationale != "" {
			fmt.Fprintf(w, "    rationale: %s\n", p.Request.Rationale)
		}
		fmt.Fprintf(w, "    files:     %d (%s)\n", len(p.Files), joinShort(p.Files, 3))
		if p.StagedAt != "" {
			fmt.Fprintf(w, "    staged:    %s\n", p.StagedAt)
		}
		fmt.Fprintln(w)
	}
	return nil
}

func writeReviewDetailHuman(w io.Writer, d *ReviewDetail, showContent bool) error {
	p := d.Proposal
	fmt.Fprintf(w, "Staging ID: %s\n", p.StagingID)
	fmt.Fprintf(w, "Staged at:  %s\n", p.StagedAt)
	fmt.Fprintf(w, "Intent:     %s\n", p.Request.Intent)
	if p.Request.Rationale != "" {
		fmt.Fprintf(w, "Rationale:  %s\n", p.Request.Rationale)
	}
	if p.Request.Confidence != "" {
		fmt.Fprintf(w, "Confidence: %s\n", p.Request.Confidence)
	}
	fmt.Fprintf(w, "Routing:    %s — %s\n", p.Routing.Mode, p.Routing.Reason)

	if len(p.Request.Sources) > 0 {
		fmt.Fprintln(w, "Sources:")
		for _, s := range p.Request.Sources {
			ref := s.Ref
			if ref == "" {
				ref = "(no ref)"
			}
			fmt.Fprintf(w, "  - %s: %s\n", s.Type, ref)
		}
	}

	fmt.Fprintln(w, "Files:")
	for _, f := range p.Files {
		fmt.Fprintf(w, "  - %s\n", f)
	}

	if len(d.Targets) > 0 {
		fmt.Fprintln(w, "Drift targets:")
		for _, t := range d.Targets {
			line := fmt.Sprintf("  - %s", t.Path)
			if t.SectionID != "" {
				line += fmt.Sprintf(" (section: %s)", t.SectionID)
			}
			line += fmt.Sprintf(" [%s]", t.Policy)
			fmt.Fprintln(w, line)
			if t.Hash != "" {
				fmt.Fprintf(w, "      expected hash: %s\n", t.Hash)
			}
		}
	}

	if showContent && len(d.Files) > 0 {
		fmt.Fprintln(w)
		for rel, body := range d.Files {
			fmt.Fprintf(w, "=== STAGED %s ===\n", rel)
			fmt.Fprint(w, body)
			if len(body) > 0 && body[len(body)-1] != '\n' {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "=== END %s ===\n\n", rel)
		}
	}
	return nil
}

// joinShort joins up to n elements of paths with ", " and appends "+K more"
// when truncated.
func joinShort(paths []string, n int) string {
	if len(paths) <= n {
		out := ""
		for i, p := range paths {
			if i > 0 {
				out += ", "
			}
			out += p
		}
		return out
	}
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ", "
		}
		out += paths[i]
	}
	out += fmt.Sprintf(", +%d more", len(paths)-n)
	return out
}
