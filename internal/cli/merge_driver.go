package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	agentgit "github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/markdown"
)

// NewMergeDriverCmd returns the `agent-memory merge-driver` subcommand. It
// has two modes:
//
//   - git driver:  `agent-memory merge-driver <base> <ours> <theirs> [path]`
//     performs a section-aware 3-way merge and writes the result to <ours>
//     (the path git passes as %A). Exits non-zero if conflicts remain.
//   - setup:       `agent-memory merge-driver --install` registers the driver
//     in this repo (writes .agent-memory/.gitattributes + git config).
func NewMergeDriverCmd() *cobra.Command {
	var (
		rootFlag string
		install  bool
	)
	cmd := &cobra.Command{
		Use:   "merge-driver [BASE OURS THEIRS [PATH]]",
		Short: "Section-aware git merge driver for .agent-memory/ files",
		Long: `Lets two branches' edits to a shared .agent-memory/ file merge by
section (@id) instead of producing whole-file conflict markers. Both
branches appending different decisions/pitfalls union cleanly; only edits
to the SAME section by both sides produce a conflict block for review.

Setup (run once per clone — git config is per-clone and not committable):

  agent-memory merge-driver --install

This writes ".agent-memory/.gitattributes" (committed: *.md merge=agent-memory)
and registers "merge.agent-memory.driver" in the repo's git config. After a
teammate clones the repo (which carries the .gitattributes), they run the
same --install to register the driver locally.

git invokes the driver form itself; you rarely type it:

  agent-memory merge-driver %O %A %B %P`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if install {
				return runInstallMergeDriver(rootFlag, cmd.OutOrStdout())
			}
			if len(args) < 3 {
				return fmt.Errorf("merge-driver needs BASE OURS THEIRS [PATH] (git supplies them), or pass --install")
			}
			return runMergeDriver(args[0], args[1], args[2])
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&install, "install", false, "register the merge driver in this repo (git config + .gitattributes)")
	return cmd
}

// runMergeDriver reads the three versions, merges section-aware, and writes
// the result back to the ours path (git's %A). A parse failure or remaining
// conflict returns an error → non-zero exit, which git reads as "conflict".
func runMergeDriver(basePath, oursPath, theirsPath string) error {
	base, err := os.ReadFile(basePath)
	if err != nil {
		return fmt.Errorf("merge-driver: read base: %w", err)
	}
	ours, err := os.ReadFile(oursPath)
	if err != nil {
		return fmt.Errorf("merge-driver: read ours: %w", err)
	}
	theirs, err := os.ReadFile(theirsPath)
	if err != nil {
		return fmt.Errorf("merge-driver: read theirs: %w", err)
	}

	result, conflicted, warnings, err := markdown.Merge3Way(base, ours, theirs)
	if err != nil {
		// Couldn't parse as Markdown sections — leave ours untouched and
		// signal conflict so git falls back to a manual resolution.
		return fmt.Errorf("merge-driver: %w", err)
	}
	if err := os.WriteFile(oursPath, result, 0o644); err != nil {
		return fmt.Errorf("merge-driver: write result: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "merge-driver: "+w)
	}
	if conflicted {
		// Non-zero exit: git keeps the file unmerged with the @merge-conflict
		// blocks for a human to resolve.
		return fmt.Errorf("merge-driver: unresolved conflicts remain — resolve the @merge-conflict block(s) in %s", oursPath)
	}
	return nil
}

// runInstallMergeDriver registers the driver for the repo at rootFlag.
func runInstallMergeDriver(rootFlag string, w io.Writer) error {
	memDir, err := reviewMemDir(rootFlag) // resolves root + checks .agent-memory/ exists
	if err != nil {
		return err
	}
	root := filepath.Dir(memDir)

	gaPath := filepath.Join(memDir, ".gitattributes")
	if err := ensureLine(gaPath, "*.md merge=agent-memory"); err != nil {
		return fmt.Errorf("merge-driver --install: %w", err)
	}
	if err := agentgit.InstallMergeDriver(root); err != nil {
		return fmt.Errorf("merge-driver --install: %w", err)
	}

	fmt.Fprintln(w, "Installed the agent-memory merge driver:")
	fmt.Fprintln(w, "  .agent-memory/.gitattributes  → *.md merge=agent-memory  (commit this)")
	fmt.Fprintln(w, "  git config                    → merge.agent-memory.driver  (local to this clone)")
	fmt.Fprintln(w, "Teammates run `agent-memory merge-driver --install` once after cloning.")
	return nil
}

// ensureLine makes sure file contains line (exactly, on its own line),
// creating or appending as needed. Idempotent.
func ensureLine(path, line string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range splitLines(string(existing)) {
		if l == line {
			return nil // already present
		}
	}
	out := string(existing)
	if len(out) > 0 && !endsWithNewline(out) {
		out += "\n"
	}
	out += line + "\n"
	return os.WriteFile(path, []byte(out), 0o644)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, trimCR(s[start:]))
	}
	return lines
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

func endsWithNewline(s string) bool { return len(s) > 0 && s[len(s)-1] == '\n' }
