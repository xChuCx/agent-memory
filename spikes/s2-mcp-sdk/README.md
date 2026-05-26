# Spike S2 — Go MCP SDK Familiarization

**Purpose:** Confirm `github.com/modelcontextprotocol/go-sdk/mcp` is sufficient for our three-tool MCP server design (`memory.fetch_context`, `memory.propose_update`, `memory.status`). Builds a minimal stdio server with one dummy `ping` tool and verifies an agent (Claude Code) can call it end-to-end.

**Why this is the bet (the product bet, not just technical):** S1 proved the byte-preserving engine (the technical bet). S2 proves the agent-facing surface — the transport and tool-registration shape. Together they unlock M1.

## How to build

From the repository root, with Go 1.22+ installed:

```powershell
go get github.com/modelcontextprotocol/go-sdk/mcp@latest
go mod tidy
go build -o ./bin/s2-spike.exe ./spikes/s2-mcp-sdk
```

On Linux/macOS the output is `./bin/s2-spike` (no `.exe`).

If `go get @latest` upgrades beyond v1.4.0, that's fine — note the version in [s2-results.md](../../docs/spikes/s2-results.md) under findings.

## How to test against Claude Code

1. Build the binary (above).

2. Add the server to Claude Code's MCP configuration. For project-level:

   `.claude/settings.local.json`:
   ```json
   {
     "mcpServers": {
       "agent-memory-s2": {
         "command": "I:/agent-memory/bin/s2-spike.exe"
       }
     }
   }
   ```

   Adjust the path. On POSIX use `./bin/s2-spike`.

3. Restart Claude Code (or reload MCP servers from settings).

4. In a Claude Code session, ask Claude:

   > Use the agent-memory-s2 ping tool with name "world".

5. Verify the response contains `pong: world` and a current ISO-8601 timestamp (server_time).

## What the spike answers

- **Module path correctness.** Is `github.com/modelcontextprotocol/go-sdk/mcp` the right import?
- **Server lifecycle.** Does `NewServer` + `Run(ctx, &StdioTransport{})` boot a clean server and shut down on stdin EOF?
- **Tool registration.** Does `mcp.AddTool[Input, Output]` work as expected with the generic handler signature?
- **Schema derivation.** Are JSON schemas derived from `json:` + `jsonschema:` struct tags accurate enough for Claude Code to invoke the tool?
- **Output binding.** Does Claude correctly surface the structured `PingOutput` to the user?
- **Error handling.** What happens if the handler returns a Go error?

Findings go into [s2-results.md](../../docs/spikes/s2-results.md).

## Files

- `main.go` — the spike server (~55 lines).

## See also

- [Pattern: MCP Tool Server](../../docs/patterns/mcp-tool-server.md)
- [Spike S2 Results](../../docs/spikes/s2-results.md)
- [Design Doc v0.4.1 §14, §15](../../agent-memory-design-doc-v0.4.1.md) — three real tools and their contracts.
- [Implementation Plan §3 S2](../../agent-memory-implementation-plan.md).
- [Go MCP SDK on GitHub](https://github.com/modelcontextprotocol/go-sdk)
