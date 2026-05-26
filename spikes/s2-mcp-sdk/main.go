// Spike S2: minimal MCP stdio server exposing one dummy "ping" tool.
//
// Purpose: confirm github.com/modelcontextprotocol/go-sdk/mcp is sufficient
// for the three-tool design (memory.fetch_context, memory.propose_update,
// memory.status). The ping tool exercises the same machinery — generic
// tool registration with struct-tag-derived JSON schemas, structured
// output, stdio transport lifecycle — without needing any real backend.
//
// See ../../docs/spikes/s2-results.md and ../../docs/patterns/mcp-tool-server.md.
package main

import (
	"context"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PingInput is the input schema for the ping tool.
// Struct tags drive JSON encoding and JSON Schema generation.
type PingInput struct {
	Name string `json:"name" jsonschema:"name to include in the pong response"`
}

// PingOutput is the structured result.
type PingOutput struct {
	Pong       string `json:"pong" jsonschema:"echo of the input name"`
	ServerTime string `json:"server_time" jsonschema:"server-side ISO-8601 UTC timestamp"`
}

// handlePing is the tool handler. The SDK derives the input schema from
// PingInput and serializes PingOutput as the structured result.
func handlePing(ctx context.Context, req *mcp.CallToolRequest, input PingInput) (
	*mcp.CallToolResult,
	PingOutput,
	error,
) {
	return nil, PingOutput{
		Pong:       input.Name,
		ServerTime: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "agent-memory-s2-spike",
		Version: "0.1.0-spike",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Return a pong echo of the given name plus the server's current UTC time. Used to verify MCP wiring end-to-end.",
	}, handlePing)

	// stdin EOF (Claude Code closing the server) ends Run cleanly.
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("mcp server exited with error: %v", err)
	}
}
