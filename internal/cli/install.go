package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/adapters/claude"
)

// InstallResult is the structured shape returned by `agent-memory install`.
// Unified across adapters so consumers parse the same JSON regardless of
// which adapter was requested.
type InstallResult struct {
	Adapter string   `json:"adapter"`
	Files   []string `json:"files,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// supportedAdapters lists the adapter identifiers `install` accepts.
// Future adapters (cursor, codex, ...) extend this slice and the
// dispatch switch in runInstall.
var supportedAdapters = []string{claude.AdapterName}

// NewInstallCmd returns the `agent-memory install` subcommand.
func NewInstallCmd() *cobra.Command {
	var (
		rootFlag   string
		userGlobal bool
		force      bool
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "install ADAPTER",
		Short: "Install an agent-runtime adapter (e.g., claude)",
		Long: `Materialises the agent-facing assets for a given runtime adapter so
the agent learns when and how to call agent-memory's MCP tools.

For the claude adapter: writes a SKILL.md file to the project's
.claude/skills/agent-memory/ directory (or to ~/.claude/skills/... with
--user-global). Claude Code picks the skill up automatically.

Existing skill files are preserved unless --force is set. Re-running
without --force is safe — already-installed files are reported under
"skipped" and nothing is overwritten.

Supported adapters: ` + strings.Join(supportedAdapters, ", "),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := runInstall(installOptions{
				Adapter:    args[0],
				Root:       rootFlag,
				UserGlobal: userGlobal,
				Force:      force,
			})
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeInstallHuman(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root for project-local install (default: current working directory)")
	cmd.Flags().BoolVar(&userGlobal, "user-global", false, "install under the user's home directory instead of the repo root")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing skill files")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	return cmd
}

type installOptions struct {
	Adapter    string
	Root       string
	UserGlobal bool
	Force      bool
}

// runInstall dispatches on the adapter name. Each adapter implements its
// own filesystem layout; runInstall maps the CLI flags onto that adapter's
// Options struct.
func runInstall(opts installOptions) (*InstallResult, error) {
	switch opts.Adapter {
	case claude.AdapterName:
		root := opts.Root
		if !opts.UserGlobal && root == "" {
			r, err := resolveRoot("")
			if err != nil {
				return nil, err
			}
			root = r
		}
		res, err := claude.Install(claude.Options{
			Root:       root,
			UserGlobal: opts.UserGlobal,
			Force:      opts.Force,
		})
		if err != nil {
			return nil, fmt.Errorf("install %s: %w", opts.Adapter, err)
		}
		return &InstallResult{
			Adapter: res.Adapter,
			Files:   res.Files,
			Skipped: res.Skipped,
		}, nil
	default:
		return nil, fmt.Errorf("install: unknown adapter %q (supported: %s)",
			opts.Adapter, strings.Join(supportedAdapters, ", "))
	}
}

func writeInstallHuman(w io.Writer, res *InstallResult) error {
	switch {
	case len(res.Files) > 0:
		fmt.Fprintf(w, "Installed %s adapter:\n", res.Adapter)
		for _, f := range res.Files {
			fmt.Fprintf(w, "  wrote: %s\n", f)
		}
	case len(res.Skipped) > 0:
		fmt.Fprintf(w, "%s adapter already installed:\n", res.Adapter)
		for _, f := range res.Skipped {
			fmt.Fprintf(w, "  preserved: %s\n", f)
		}
		fmt.Fprintln(w, "Pass --force to overwrite.")
	default:
		fmt.Fprintf(w, "Nothing to do for %s adapter.\n", res.Adapter)
	}
	return nil
}
