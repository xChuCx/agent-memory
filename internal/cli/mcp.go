package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	agentmcp "github.com/xChuCx/agent-memory/internal/mcp"
)

// NewMCPCmd returns the `agent-memory mcp` subcommand. With --stdio (the
// default and only currently-supported transport) it starts a JSON-RPC
// MCP server on stdio and registers the agent-facing tools.
//
// M2 registers memory.fetch_context. M3 will add memory.propose_update
// and memory.status.
func NewMCPCmd() *cobra.Command {
	var (
		rootFlag string
		stdio    bool
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the agent-memory MCP server",
		Long: `Starts the JSON-RPC MCP server that exposes the agent-facing
tools to clients like Claude Code.

Currently only the stdio transport is supported. The server runs until
the client closes the stdin channel.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveMCPRoot(rootFlag)
			if err != nil {
				return err
			}
			srv := agentmcp.New(root, Version())
			if err := srv.RegisterTools(); err != nil {
				return err
			}
			return srv.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: $CLAUDE_PROJECT_DIR, else current working directory)")
	cmd.Flags().BoolVar(&stdio, "stdio", true, "use the stdio transport (currently the only option)")
	return cmd
}

// resolveMCPRoot resolves the repo root the MCP server serves. Precedence:
//
//  1. the --root flag (explicit wins);
//  2. $CLAUDE_PROJECT_DIR — Claude Code sets this in a spawned stdio server's
//     environment to the project root. Crucially it does NOT change the
//     server's working directory, so a server registered as `agent-memory mcp`
//     with no --root must read this env var to find the right repo;
//  3. the current working directory (manual runs outside an MCP client).
//
// This is what lets ONE registration (e.g. a project .mcp.json with
// `--root ${CLAUDE_PROJECT_DIR:-.}`, or a server with no --root at all) serve
// the repo the agent is actually in — instead of being pinned to one repo at
// spawn time, which silently routed every project's writes to a single store.
func resolveMCPRoot(flag string) (string, error) {
	if flag == "" {
		if env := strings.TrimSpace(os.Getenv("CLAUDE_PROJECT_DIR")); env != "" {
			flag = env
		}
	}
	return resolveRoot(flag)
}
