# Module: internal/mcp
<!-- @id: module-mcp -->

The stdio MCP server that exposes the three agent-facing tools. Thin
wrapper over the official go-sdk; reuses `internal/memory` so behaviour
stays in lockstep with the CLI.

## server + tool registration
<!-- @id: mcp-server -->

`server.go`: `New(root, version)` builds the SDK server and a stderr
logger (`logging.FromEnv(os.Stderr)`), `RegisterTools` attaches the three
tools, `Run` serves over `mcp.StdioTransport`. The root is re-read each
call so live edits to `.agent-memory/` are picked up without a restart.
**Sources:** internal/mcp/server.go

## the three tools
<!-- @id: mcp-tools -->

- `memory.fetch_context` (`tools.go`) — budgeted, ranked Markdown pack;
  empty query returns the bootstrap pack.
- `memory.propose_update` (`propose.go`) — the only write tool; a
  rejection is a successful JSON-RPC response carrying the reason, not a
  transport error.
- `memory.status` (`status.go`) — read-only health report; intentionally
  quiet (no logger).

Each `runX` handler loads manifest/schema/index, calls into
`internal/memory`, and maps to a typed output struct. The fetch/propose
handlers receive the server's stderr logger; status does not.
**Sources:** internal/mcp/tools.go, internal/mcp/propose.go, internal/mcp/status.go
