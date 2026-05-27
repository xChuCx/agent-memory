// Package mcp implements the stdio MCP server that exposes the agent-facing
// tools (memory.fetch_context in M2; memory.propose_update and memory.status
// in M3).
//
// The server reuses the same loading/fetch logic as the CLI (internal/cli
// and internal/memory) so behaviour stays in lockstep between transports.
//
// See docs/patterns/mcp-tool-server.md and spike S2 results.
package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is a thin wrapper over mcp.Server that knows about the agent-memory
// repo root. Tools registered on it read the root each time a call comes in,
// so the user can edit .agent-memory/ files between invocations without
// restarting the server.
type Server struct {
	server  *mcp.Server
	root    string // absolute path to the repo root
	version string
}

// New constructs a Server but does not start it. Call RegisterTools then Run.
func New(root, version string) *Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-memory",
		Version: version,
	}, nil)
	return &Server{
		server:  srv,
		root:    root,
		version: version,
	}
}

// RegisterTools attaches the agent-facing tools to the server. M2 registers
// memory.fetch_context only; M3 will add propose_update and status.
func (s *Server) RegisterTools() error {
	if err := registerFetchContext(s.server, s.root); err != nil {
		return fmt.Errorf("register memory.fetch_context: %w", err)
	}
	return nil
}

// Run blocks until the stdio transport is closed (e.g., Claude Code shuts
// the server down by closing stdin). Returns the first non-nil error from
// the transport.
func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}
