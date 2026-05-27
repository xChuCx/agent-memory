// Package cli wires up the cobra command tree for the agent-memory binary.
// M0 ships only the `version` subcommand; M1+ add `init`, `status`, `fetch`,
// `review`, `apply`, `reject`, `rebase`, `rebuild-index`, `doctor`, `mcp`,
// and `install <adapter>`.
package cli

import (
	"github.com/spf13/cobra"
)

// ProgramVersion is the user-visible version string for `agent-memory version`.
// Follows semver; bumped at every release tag. Decoupled from DesignDocVersion
// — the design doc has its own version line that tracks the spec, while the
// program version tracks shipped binaries.
const ProgramVersion = "0.1.0"

// DesignDocVersion is the spec revision this binary implements. Printed
// alongside ProgramVersion in `status --json` and useful for matching a
// binary's behaviour back to a written design.
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
	root.AddCommand(NewInstallCmd())

	return root
}
