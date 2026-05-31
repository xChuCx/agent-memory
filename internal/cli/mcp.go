package cli

import (
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
			root, err := resolveRoot(rootFlag)
			if err != nil {
				return err
			}
			srv := agentmcp.New(root, ProgramVersion)
			if err := srv.RegisterTools(); err != nil {
				return err
			}
			return srv.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().BoolVar(&stdio, "stdio", true, "use the stdio transport (currently the only option)")
	return cmd
}
