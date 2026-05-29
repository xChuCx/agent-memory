// Package cli wires up the cobra command tree for the agent-memory binary.
// M0 ships only the `version` subcommand; M1+ add `init`, `status`, `fetch`,
// `review`, `apply`, `reject`, `rebase`, `rebuild-index`, `doctor`, `mcp`,
// and `install <adapter>`.
package cli

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/logging"
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
//	go build -ldflags='-X github.com/agent-memory/agent-memory/internal/cli.ProgramVersion=v0.3.0' ./cmd/agent-memory
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
		Short: "Local context middleware for AI coding agents",
		Long: `agent-memory is a local context router and memory safety layer for AI
coding agents. It maintains a structured, branch-aware, byte-preserving
Markdown memory layer for a repository and exposes it via three MCP tools.

Design and roadmap: see agent-memory-design-doc-v0.4.1.md and
agent-memory-implementation-plan.md in the repository root.`,
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
	root.AddCommand(NewReviewCmd())
	root.AddCommand(NewApplyCmd())
	root.AddCommand(NewRejectCmd())
	root.AddCommand(NewSweepCmd())
	root.AddCommand(NewRebaseCmd())
	root.AddCommand(NewRebuildIndexCmd())
	root.AddCommand(NewInstallCmd())

	return root
}
