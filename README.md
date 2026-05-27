# agent-memory

Local context middleware for AI coding agents. One MCP call in, structured
memory updates out. Branch-aware. Secret-safe. Byte-preserving. Two tools.

## Status

**Release 0.1** — first stable cut. Implements the Core Contract from
[Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md): an MCP server
that reads project memory and writes durable knowledge back, with
structured operations, drift-checked staging, secret scanning, and a
Claude Code adapter.

See [CHANGELOG.md](CHANGELOG.md) for what's in 0.1 and what's deferred to
0.2 / 0.3 (git auto-stage, staging TTL sweeper, rebuild-index, additional
agent-runtime adapters).

| Document | Purpose |
|---|---|
| [CHANGELOG.md](CHANGELOG.md) | Per-release feature list and known limitations. |
| [Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md) | Canonical design this binary implements. |
| [Implementation Plan](agent-memory-implementation-plan.md) | Release cuts and milestone breakdown. |
| [Patterns](docs/patterns/) | Reusable design patterns documented per subsystem. |
| [Spikes](docs/spikes/) | Pre-M1 spike outcomes (byte-preserving engine, MCP SDK, flock, FTS5). |

Older revisions preserved for traceability:

- [Design Doc v0.4](agent-memory-design-doc-v0.4.md)
- [Design Doc v0.3](agent-memory-design-doc-v0.3.md)

## Quick start

```bash
# Build
go build -o agent-memory ./cmd/agent-memory

# Scaffold .agent-memory/ in a repo
./agent-memory init --name my-project

# Install the Claude Code skill (writes .claude/skills/agent-memory/SKILL.md)
./agent-memory install claude

# Verify
./agent-memory version
# → 0.1.0

# Read context
./agent-memory fetch                # bootstrap pack
./agent-memory fetch "auth"         # FTS query

# Start MCP server (Claude Code spawns this automatically once configured)
./agent-memory mcp
```

Configure Claude Code to spawn the MCP server — add to
`~/.claude/mcp_servers.json` (or the project-local equivalent):

```json
{
  "mcpServers": {
    "agent-memory": {
      "command": "/abs/path/to/agent-memory",
      "args": ["mcp"]
    }
  }
}
```

## Build

Requires Go 1.25+ (the MCP SDK transitively requires it).

```bash
go build -o agent-memory ./cmd/agent-memory   # binary
go test ./...                                  # unit + integration tests
go test -tags=e2e ./internal/e2e/...           # end-to-end smoke (linux/macos)
go test -race ./internal/...                   # race detector
```

`make` targets are equivalent to the `go` commands above; see the
`Makefile` if you prefer that style.

## CLI

```bash
agent-memory init [--root DIR] [--name NAME] [--force]
        # Create the .agent-memory/ scaffold.

agent-memory status [--root DIR] [--json]
        # Project state: version, file counts per category, lock metadata.

agent-memory doctor [--root DIR]
        # Diagnostic layout checks. Advisory; exits 0 even with findings.

agent-memory fetch [QUERY] [--scope X,Y] [--budget N]
                   [--exclude-archive] [--json] [--root DIR]
        # Return a budgeted Markdown context pack.

agent-memory mcp [--root DIR]
        # Start the MCP server (stdio). Exposes memory.fetch_context and
        # memory.propose_update.

agent-memory review [STAGING_ID] [--show] [--json] [--root DIR]
        # List staged proposals or inspect one.

agent-memory apply STAGING_ID [--json] [--root DIR]
        # Re-validate drift and apply a staged proposal.

agent-memory reject STAGING_ID [--json] [--root DIR]
        # Discard a staged proposal.

agent-memory install <adapter> [--user-global] [--force] [--json]
        # Materialise agent-runtime adapter assets. Supported: claude.

agent-memory version
        # Print binary version and exit.
```

## MCP tools

Exposed by `agent-memory mcp` over stdio JSON-RPC:

| Tool | Purpose |
|------|---------|
| `memory.fetch_context` | Read a budgeted Markdown context pack. |
| `memory.propose_update` | Submit structured edits (apply or stage). |

The Claude Code skill installed by `agent-memory install claude`
documents when and how an agent should call each one.

## Architecture (at a glance)

```
.agent-memory/
├── meta/
│   ├── manifest.yaml      operational settings (budgets, approval, security)
│   ├── schema.yaml        per-category file/glob, section schema, provenance
│   ├── index.sqlite       FTS5 shadow index (regenerable)
│   ├── lock               OS-level advisory lock (flock)
│   └── lock.info          informational metadata sidecar
├── conventions.md         project conventions
├── decisions.md           durable architectural decisions
├── pitfalls.md            known footguns
├── index.md               server-managed memory index summary
├── modules/<name>.md      per-module facts
├── archive/<date>-*.md    write-once archived entries
├── local/
│   ├── current.shared.md  cross-branch working notes
│   └── current.<branch>.md branch-scoped working notes
├── sessions/<YYYY-MM-DD>.md per-day session logs
└── staging/<id>/          pending human-review proposals
    ├── proposal.json
    ├── target-checksums.json
    └── files/<rel-path>
```

## Layout

```
cmd/agent-memory/                       CLI entry point
internal/
  adapters/claude/                      embedded SKILL.md + Install()
  cli/                                  cobra subcommands
  config/ schema/                       YAML loaders (manifest + schema)
  e2e/                                  release-0.1 smoke test (-tags=e2e)
  fs/                                   atomic writes, path validation
  git/                                  branch resolver
  index/                                FTS5 incremental index
  lock/                                 flock-based advisory lock
  markdown/                             byte-preserving Markdown engine
  mcp/                                  stdio MCP server
  memory/                               operations, security, orchestrator, staging
spikes/                                 pre-M1 spike investigations (S1-S4)
docs/
  patterns/                             design patterns
  spikes/                               spike outcome docs
.github/workflows/ci.yml                CI: tests + e2e + lint
agent-memory-design-doc-v0.4.1.md       canonical design
agent-memory-implementation-plan.md     build plan
CHANGELOG.md                            per-release feature list
```

## License

TBD. See [Implementation Plan §18 Open Decisions](agent-memory-implementation-plan.md).
