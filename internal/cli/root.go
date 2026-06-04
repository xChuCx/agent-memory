// Package cli wires up the cobra command tree for the agent-memory binary.
// M0 ships only the `version` subcommand; M1+ add `init`, `status`, `fetch`,
// `review`, `apply`, `reject`, `rebase`, `rebuild-index`, `doctor`, `mcp`,
// and `install <adapter>`.
package cli

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/logging"
)

// logLevelFlag is bound to the root's persistent --log-level flag. Empty
// → fall back to $AGENT_MEMORY_LOG, then WARN. Read by cliLogger().
var logLevelFlag string

// cliLogger builds the logger CLI commands thread into their deps. It
// always writes to STDERR — stdout is reserved for command output
// (Markdown packs, --json payloads). Level resolves --log-level, then
// $AGENT_MEMORY_LOG, then WARN.
func cliLogger() *slog.Logger {
	level := logging.ParseLevel(logLevelFlag, logging.LevelFromEnv(slog.LevelWarn))
	return logging.New(os.Stderr, level)
}

// ProgramVersion is the user-visible version string for `agent-memory version`.
// Default value is "dev" so `go build ./cmd/agent-memory` produces a binary
// that clearly identifies itself as a development build. Release builds via
// goreleaser stamp the actual git tag in via -ldflags, e.g.:
//
//	go build -ldflags='-X github.com/xChuCx/agent-memory/internal/cli.ProgramVersion=v0.3.0' ./cmd/agent-memory
//
// See .goreleaser.yml and .github/workflows/release.yml.
//
// var, not const, so the linker can override it. The exported name is
// stable; only the value is configurable at build time.
var ProgramVersion = "dev"

// DesignDocVersion is the spec revision this binary implements. Printed
// alongside ProgramVersion in `status --json` and useful for matching a
// binary's behaviour back to a written design. Hardcoded — the spec only
// moves with documented design-doc bumps, not per release.
const DesignDocVersion = "v0.4.1"

// NewRootCmd builds the agent-memory root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "agent-memory",
		Short: "Local, git-native memory for AI coding agents",
		Long: `agent-memory gives AI coding agents a durable, reviewable memory for a
repository: current task state, decisions, conventions, pitfalls, and
per-module facts kept as plain Markdown under .agent-memory/ — committed to
your repo, not shipped to a cloud. Agents read and write it over three MCP
tools (memory.fetch_context, memory.propose_update, memory.status); durable
changes stage for your review before they land.

Typical flow:
  1. agent-memory init              scaffold .agent-memory/ in the repo
  2. agent-memory install claude    teach your agent the tools (or cursor|agents|gemini)
  3. your agent spawns 'agent-memory mcp' and calls fetch_context / propose_update
  4. agent-memory review --diff && agent-memory apply   inspect and land staged changes

No MCP server handy? 'agent-memory propose' is the CLI front door to the same
write pipeline. Run 'agent-memory <command> --help' for any command; see the
README and ROADMAP.md for the bigger picture.`,
		Example: `  # one-time setup in a repo
  agent-memory init --name my-project
  agent-memory install claude

  # read the current context pack (empty query returns the bootstrap pack)
  agent-memory fetch
  agent-memory fetch "auth token rotation" --scope internal/auth

  # check memory health (file counts, staged proposals, drift, lock)
  agent-memory status

  # land any staged proposals after reviewing the exact diff
  agent-memory review --latest --diff
  agent-memory apply --latest`,
		// Suppress cobra's default behavior of printing usage on every error;
		// we surface errors ourselves from main.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&logLevelFlag, "log-level", "",
		"log verbosity to stderr: debug|info|warn|error (default warn, or $AGENT_MEMORY_LOG)")

	root.AddCommand(NewVersionCmd())
	root.AddCommand(NewInitCmd())
	root.AddCommand(NewStatusCmd())
	root.AddCommand(NewDoctorCmd())
	root.AddCommand(NewFetchCmd())
	root.AddCommand(NewMCPCmd())
	root.AddCommand(NewProposeCmd())
	root.AddCommand(NewReviewCmd())
	root.AddCommand(NewApplyCmd())
	root.AddCommand(NewRejectCmd())
	root.AddCommand(NewSweepCmd())
	root.AddCommand(NewRebaseCmd())
	root.AddCommand(NewRebuildIndexCmd())
	root.AddCommand(NewInstallCmd())
	root.AddCommand(NewMergeDriverCmd())
	root.AddCommand(NewStoreCmd())

	return root
}
