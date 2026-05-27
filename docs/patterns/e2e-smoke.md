# Pattern: End-to-End Smoke Test (Release 0.1 Verification)

**Status:** Implemented in [`internal/e2e/release01_smoke_test.go`](../../internal/e2e/release01_smoke_test.go).
**Owner:** `internal/e2e/` (Release 0.1 verification).
**CI integration:** `.github/workflows/ci.yml` `e2e` job.

## Problem

Unit tests prove each package works in isolation, but Release 0.1 is a
distributed system in miniature: a CLI binary that ships an MCP server,
a staging directory format both halves read and write, an FTS5 index
that has to stay coherent across propose/apply cycles, and an embedded
SKILL.md that teaches an agent to use the whole thing.

A passing unit suite does not prove:

- The compiled binary actually starts.
- `agent-memory mcp` over stdio is reachable by a real MCP client.
- Bytes round-trip through propose → stage → apply intact.
- The FTS index gets re-built on `apply` so the next `fetch` sees the
  new content.
- Rejection paths surface through the MCP wire layer the same way they
  surface through internal-package tests.

## Solution

A single Go test under `internal/e2e/`, build-tagged `e2e`, that:

1. Compiles the production binary once in `TestMain`.
2. For each subtest, invokes the binary as a subprocess with a fresh
   `t.TempDir()` for the project root.
3. Drives `agent-memory mcp` via the **official MCP SDK's client + 
   `CommandTransport`** — no hand-rolled JSON-RPC framing.

The test mirrors a realistic user session:

```
1.  init                          .agent-memory/ tree exists
2.  install claude                SKILL.md at the documented path
3.  fetch (empty query)           bootstrap pack non-empty + has Conventions
4.  mcp propose record_decision   status: staged, staging_id returned
5.  review (json)                 staging_id is in the list
6.  review <id> --show            Files map has decisions.md with the section
7.  apply <id>                    file on disk has the section, staging gone
8.  status (json)                 categories.decisions >= 1
9.  fetch "smoke" (query)         FTS picks up the applied decision
10. mcp propose with AKIA secret  rejected in body (not transport error)
                                  no token bytes leak via Type/Location
11. mcp propose + reject <id>     staging directory removed
```

Plus a separate `TestRelease01_FetchLatencyUnderHalfSecond` as a
regression guard against fetch performance silently degrading.

## Why `//go:build e2e`

`go test ./...` must stay fast — unit tests run on every save, every
pre-commit, every PR push. The e2e suite is:

- Slower (compiles the binary, spawns subprocesses, hits SQLite).
- Larger blast radius if it flakes (a flake here blocks all PRs, not
  one package).
- Linux-only in CI (subprocess + SQLite warmup is more expensive; the
  per-OS matrix already covers compatibility of the inner packages).

The tag wall makes it explicitly opt-in: `go test -tags=e2e ./...` or
the dedicated `e2e` CI job.

## Subprocess invocations

A small helper does the heavy lifting:

```go
func run(t *testing.T, root string, args ...string) (stdout, stderr string, err error)
func runJSON(t *testing.T, v any, root string, args ...string)
```

`run` execs the binary with `Dir: root` so `--root` need not be passed
every time (the CLI defaults to the working directory). `runJSON`
appends `--json` and decodes stdout — most CLI commands now have a
`--json` output mode specifically so e2e tests can assert on structured
data instead of parsing prose.

## MCP driving via `CommandTransport`

The Model Context Protocol SDK ships both a server and a client. The
client's `CommandTransport` wraps an `exec.Cmd` and pipes JSON-RPC
through its stdin/stdout — exactly what `agent-memory mcp` exposes.

```go
serverCmd := exec.CommandContext(ctx, binPath, "mcp", "--root", root)
client := mcp.NewClient(&mcp.Implementation{Name: "e2e-client"}, nil)
session, err := client.Connect(ctx, &mcp.CommandTransport{Command: serverCmd}, nil)
defer session.Close()

tools, _ := session.ListTools(ctx, &mcp.ListToolsParams{})
result, _ := session.CallTool(ctx, &mcp.CallToolParams{
    Name:      "memory.propose_update",
    Arguments: map[string]any{...},
})
```

The SDK handles framing, request IDs, the initialise handshake, and
response routing. The test reads `result.StructuredContent` as the
JSON-marshalled `ProposeUpdateOutput`. Re-marshal → re-unmarshal into a
local anonymous struct keeps the test free of imports against the
`internal/mcp` types — the e2e package treats the binary as a black
box.

## Rejection-vs-error discipline

A persistent risk in MCP servers is mixing application-level rejections
into the transport's error channel: an agent gets a JSON-RPC error
message instead of a structured "your request was bad and here's why"
response. The e2e test guards this in two places:

- **secret_detected**: a propose with `AKIAIOSFODNN7EXAMPLE` in the
  body must come back with `IsError: false`, `Status: "rejected"`,
  `Reason: "secret_detected"`, populated `Findings[]`.
- **No-token-leak**: every finding's `Type` and `ApproximateLocation`
  must be free of `AKIA` substrings. Cross-layer enforcement of the
  design doc §13.2/§23.3 rule.

## What this does NOT verify

- **Long-running session behaviour** — concurrent writes, lock
  contention under load, staging TTL expiry. Those land in M5 batch 2
  and M8.
- **Cross-OS specifics** — the SDK's `CommandTransport` skips on
  Windows for some scenarios (signal handling). Per-OS unit coverage
  + Linux-only e2e is the trade-off.
- **MCP protocol conformance** — the SDK we use IS the reference Go
  implementation; we trust its compliance. Protocol tests live
  upstream.
- **Real Claude Code integration** — testing the full chain through
  Claude Code's runtime requires Claude Code itself, which is not part
  of the CI environment. Spike S2 (`spikes/s2-mcp-sdk/`) covered the
  Claude Code wiring once, manually.

## Running locally

```bash
go test -tags=e2e -v ./internal/e2e/...
```

Set `AGENT_MEMORY_BIN` to skip the in-test compile step and reuse a
pre-built binary:

```bash
go build -o /tmp/agent-memory ./cmd/agent-memory
AGENT_MEMORY_BIN=/tmp/agent-memory go test -tags=e2e -v ./internal/e2e/...
```

## References

- [Implementation Plan §3 (release cuts)](../../agent-memory-implementation-plan.md) — Release 0.1 acceptance criteria.
- [Pattern: propose_update Pipeline](propose-update-pipeline.md).
- [Pattern: Staging Lifecycle](staging-lifecycle.md).
- [Pattern: Adapter Installation](adapter-installation.md).
- [Pattern: MCP Tool Server](mcp-tool-server.md).
