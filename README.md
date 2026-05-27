# agent-memory

Local context middleware for AI coding agents. One MCP call in, structured memory updates out. Branch-aware. Secret-safe. Byte-preserving. Three tools.

## Status

Early development. Currently in the spike phase for Release 0.1 (Core Contract Validation).

| Document | Purpose |
|---|---|
| [Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md) | Current canonical design. |
| [Implementation Plan](agent-memory-implementation-plan.md) | Release cuts (0.1 / 0.2 / 0.3) and milestone breakdown. |
| [Patterns](docs/patterns/) | Reusable design patterns documented as they are implemented. |
| [Spikes](docs/spikes/) | Pre-M1 spike outcome docs. |

Older revisions preserved for traceability:

- [Design Doc v0.4](agent-memory-design-doc-v0.4.md)
- [Design Doc v0.3](agent-memory-design-doc-v0.3.md)

## Build

Requires Go 1.22+. GNU make is optional — every target maps to a plain `go` command shown below.

```bash
make build         # produce ./agent-memory (or ./agent-memory.exe on Windows)
make test          # run all tests
make test-race     # run tests with race detector (linux/macos most reliable)
make lint          # golangci-lint, if installed
make tidy          # refresh go.mod / go.sum
```

Without make:

```bash
go build -o agent-memory ./cmd/agent-memory
go test ./...
```

Verify the build:

```bash
./agent-memory version
# 0.4.1-mvp-dev
```

## CLI (M1 + M2)

```bash
agent-memory init [--root DIR] [--name NAME] [--force]
        # Create the .agent-memory/ scaffold for the current repo.

agent-memory status [--root DIR] [--json]
        # Show repo state: project, version, file counts per category,
        # last-known lock metadata.

agent-memory doctor [--root DIR]
        # Diagnose layout issues. Advisory; exits 0 even when findings
        # are reported.

agent-memory fetch [QUERY] [--root DIR] [--budget N] [--scope X,Y] [--exclude-archive] [--json]
        # Return a budgeted Markdown context pack from .agent-memory/.
        # Empty QUERY returns the bootstrap pack (current.<branch>.md
        # + current.shared.md + conventions + index summary).

agent-memory mcp [--root DIR] [--stdio]
        # Start the MCP server (JSON-RPC over stdio). Exposes
        # memory.fetch_context for agents like Claude Code.

agent-memory version
        # Print the binary version and exit.
```

M3 will add `review`/`apply`/`reject`/`rebase` and the `memory.propose_update` MCP tool. M6 adds `install <adapter>`.

The pre-M1 spikes live under `spikes/` and run via:

```bash
go test ./spikes/...
```

See [docs/spikes/](docs/spikes/) for results and decisions.

## Layout

```
cmd/agent-memory/                       # CLI entry point (M0+)
internal/                               # Core packages (M1+)
spikes/                                 # Pre-M1 spike investigations
  s1-byte-preserving-markdown/          # S1: AST locate + byte splice
docs/
  patterns/                             # Reusable design patterns
  spikes/                               # Spike outcome docs
agent-memory-design-doc-*.md            # Design history
agent-memory-implementation-plan.md     # Build plan
```

## License

TBD. See [implementation plan §18 Open Decisions](agent-memory-implementation-plan.md).
