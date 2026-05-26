# Spike S2 — Go MCP SDK Familiarization

**Status:** Validated. Decision: **GO**.
**Started:** 2026-05-26
**Closed:** 2026-05-26
**Goal:** Confirm `github.com/modelcontextprotocol/go-sdk/mcp` is sufficient for the three-tool design. Build a minimal stdio server with one dummy `ping` tool and verify Claude Code can call it end-to-end.

## Decision: GO

The official Go MCP SDK works cleanly for our shape. Resolved SDK version: **v1.6.1** (`go get @latest` upgraded from the v1.4.0 baseline noted in the README). End-to-end Claude Code → stdio server → structured response round-trip succeeded on first attempt with zero adjustments needed.

Approach approved for M2 (fetch_context) and M3 (propose_update + status).

## How to validate

See [spikes/s2-mcp-sdk/README.md](../../spikes/s2-mcp-sdk/README.md) for build + install + test steps.

Summary:

1. `go get github.com/modelcontextprotocol/go-sdk/mcp@latest && go mod tidy`
2. `go build -o ./bin/s2-spike.exe ./spikes/s2-mcp-sdk` (or `s2-spike` on POSIX).
3. Add server to `.claude/settings.local.json` pointing at the binary.
4. Restart Claude Code.
5. Ask Claude: "Use the agent-memory-s2 ping tool with name 'world'."
6. Verify response includes `pong: world` and an ISO-8601 timestamp.

## Method

One tool only: `ping(name) -> {pong, server_time}`. Same machinery as the real tools will use; just no backend behind it.

Confirms:

- Module path `github.com/modelcontextprotocol/go-sdk/mcp` resolves.
- Server boots via `mcp.NewServer(...)` and runs over `&mcp.StdioTransport{}`.
- Tool registration via `mcp.AddTool` with generic `(InputT, OutputT)` works.
- Struct-tag schema generation (`json:` + `jsonschema:`) is acceptable to Claude Code.
- Claude surfaces the structured output to the user.
- Server shuts down cleanly on stdin EOF (when Claude Code closes the connection).

## Findings (running notes)

### 2026-05-26 — Initial implementation

Code: `spikes/s2-mcp-sdk/main.go` (~55 lines).

API confirmed via the official README on GitHub (`raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/README.md`):

- Import path: `github.com/modelcontextprotocol/go-sdk/mcp` (with the `/mcp` subpackage).
- Server constructor: `mcp.NewServer(&mcp.Implementation{Name, Version}, nil)`.
- Tool handler signature: `func(ctx, req *mcp.CallToolRequest, input InputT) (*mcp.CallToolResult, OutputT, error)`.
- Tool registration: `mcp.AddTool(server, &mcp.Tool{Name, Description}, handler)`.
- Transport: `server.Run(ctx, &mcp.StdioTransport{})`.
- README recommends "v1.4.0+" (latest MCP spec 2025-11-25 support).

go.mod pinned at v1.4.0; `go mod tidy` will upgrade to a newer compatible release if available — note the resolved version under the next finding.

Pattern doc with schema sketches for the three real tools: [mcp-tool-server.md](../patterns/mcp-tool-server.md).

### 2026-05-26 — Validated against Claude Code

Resolved SDK version: **v1.6.1** (from `go.mod` after `go mod tidy`).

Method: built `./bin/s2-spike.exe`, registered it in `.claude/settings.local.json` as `agent-memory-s2`, restarted Claude Code, asked Claude to call `ping` with `name: "world"`.

**Result:**

```json
{"pong":"world","server_time":"2026-05-26T13:32:35Z"}
```

End-to-end success on first attempt. All check points pass:

| Check | Result |
|---|---|
| Module path resolves (`github.com/modelcontextprotocol/go-sdk/mcp`) | PASS |
| `go mod tidy` resolves cleanly; v1.4.0 → v1.6.1 | PASS |
| `mcp.NewServer` + `Run(ctx, &StdioTransport{})` boots over stdio | PASS |
| `mcp.AddTool[PingInput, PingOutput]` registers the tool | PASS |
| Claude Code discovers the tool by name and description | PASS |
| Input JSON schema derived from struct tags: `name` (string, required) with correct description | PASS — schema came through verbatim as declared via `json:` + `jsonschema:` tags |
| Tool invoked when user asks in natural language | PASS — Claude routed "ping with name 'world'" to the tool directly |
| Structured output (`pong`, `server_time`) surfaced as JSON | PASS — Claude received `{"pong":"world","server_time":"2026-05-26T13:32:35Z"}` |
| Timestamp format (RFC3339 UTC) correct | PASS |

**No quirks observed.** Schema generation, error wrapping behavior, lifecycle, stdio framing — all clean on the happy path. Negative-path testing (handler error, schema violation, panic recovery) is M2/M3 work; the spike's exit criterion ("Claude calls the dummy tool successfully") is met.

## Decision outcome

**GO.** Implement the production three-tool server in `internal/mcp/` against this SDK during M2 and M3, following the patterns in [mcp-tool-server.md](../patterns/mcp-tool-server.md).

## Schema sketches for the three real tools

Documented in [mcp-tool-server.md "Schema sketches for the three real tools"](../patterns/mcp-tool-server.md). The point of putting them there now (before they're implemented in M2/M3) is to confirm that the SDK's struct-tag-based schema derivation is expressive enough for our nested types (operations, sources, findings, status sub-objects). If S2 surfaces a limitation here, it influences the M2/M3 designs.

## Next steps after GO

1. Move spike patterns into `internal/mcp/` during M2 (`fetch_context` tool) and M3 (`propose_update` + `status`).
2. Use the schema sketches as starting points for real tool definitions.
3. Wire the structured-rejection pattern (return `Output{Status: "rejected", ...}, nil` instead of Go errors for business-logic failures).
4. Tee MCP server logs to stderr (stdout is the JSON-RPC frame channel).

## Next steps if NO-GO on official SDK

1. Implement a handwritten JSON-RPC stdio loop under `internal/mcp/`.
2. Use the MCP protocol spec directly as the contract.
3. Document the fallback in [mcp-tool-server.md](../patterns/mcp-tool-server.md) Alternatives.
4. Re-run the S2 ping test against the handwritten implementation.
