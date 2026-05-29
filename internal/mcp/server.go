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
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/agent-memory/agent-memory/internal/logging"
)

// Server is a thin wrapper over mcp.Server that knows about the agent-memory
// repo root. Tools registered on it read the root each time a call comes in,
// so the user can edit .agent-memory/ files between invocations without
// restarting the server.
type Server struct {
	server  *mcp.Server
	root    string // absolute path to the repo root
	version string
	logger  *slog.Logger
}

// New constructs a Server but does not start it. Call RegisterTools then Run.
//
// The logger is built to STDERR deliberately: this process speaks JSON-RPC
// over stdout, so a log line on stdout would corrupt the protocol. Level is
// quiet by default (WARN) and opt-in via $AGENT_MEMORY_LOG. The logger is
// threaded into the write/read deps so the memory layer's structured logs
// (propose outcomes, fetch summaries) land on stderr without ever touching
// the JSON-RPC channel.
func New(root, version string) *Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-memory",
		Version: version,
	}, nil)
	return &Server{
		server:  srv,
		root:    root,
		version: version,
		logger:  logging.FromEnv(os.Stderr),
	}
}

// RegisterTools attaches the agent-facing tools to the server: the full
// three-tool surface from design §15 — memory.fetch_context (M2),
// memory.propose_update (M3 T3.9), and memory.status (M3 batch 4).
func (s *Server) RegisterTools() error {
	if err := registerFetchContext(s.server, s.root, s.logger); err != nil {
		return fmt.Errorf("register memory.fetch_context: %w", err)
	}
	if err := registerProposeUpdate(s.server, s.root, s.logger); err != nil {
		return fmt.Errorf("register memory.propose_update: %w", err)
	}
	// memory.status is read-only and intentionally quiet — no logger.
	if err := registerStatus(s.server, s.root, s.version); err != nil {
		return fmt.Errorf("register memory.status: %w", err)
	}
	return nil
}

// Run blocks until the stdio transport is closed (e.g., Claude Code shuts
// the server down by closing stdin). Returns the first non-nil error from
// the transport.
func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}
