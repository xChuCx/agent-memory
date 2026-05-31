# agent-memory

Local context middleware for AI coding agents. One MCP call in, structured
memory updates out. Branch-aware. Secret-safe. Byte-preserving. Two tools.

## Status

**Release 0.3** — the completeness-and-polish release on top of v0.2.0.
Closes the remaining design-doc gaps and hardens the everyday workflow,
much of it surfaced by dogfooding agent-memory on its own repo:

- **Full MCP surface** — `memory.status` joins `fetch_context` and
  `propose_update` as the third tool.
- **M4 archival ops** — `archive_section` / `remove_section` /
  `rename_heading`, plus a server-maintained `index.md`.
- **Security layer** — secret + PII scanning with allowlist size limits;
  real per-section schema validation.
- **Observability** — structured `slog` logging (stderr-only, secret-safe).
- **Smarter retrieval** — Jaccard dedup, the §20.4 ranking signals,
  OR-match recall, crash-safe FTS queries.
- **Fuller CLI** — `propose` (write without an MCP server), `review --diff`,
  staging-id prefixes + `--latest`.

The Core Contract from v0.1.0 (Design Doc v0.4.1: MCP server, structured
operations, drift-checked staging, secret scanning, Claude Code adapter)
is unchanged — all 0.3 work is additive. The git merge driver and a
behavioural eval harness remain deferred.

See [CHANGELOG.md](CHANGELOG.md) for the full 0.3 changelist.

| Document | Purpose |
|---|---|
| [ROADMAP.md](ROADMAP.md) | Where the project is going, principles, and non-goals. |
| [CHANGELOG.md](CHANGELOG.md) | Per-release feature list and known limitations. |
| [Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md) | Canonical design this binary implements. |
| [Implementation Plan](agent-memory-implementation-plan.md) | Historical MVP build log (M0–M8); see ROADMAP for what's next. |
| [Patterns](docs/patterns/) | Reusable design patterns documented per subsystem. |
| [Spikes](docs/spikes/) | Pre-M1 spike outcomes (byte-preserving engine, MCP SDK, flock, FTS5). |

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
# → 0.3.0

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

agent-memory propose --intent INTENT --op OP --path PATH [op flags...]
                     [--content STR | --content-file FILE|-] [--source type:ref]
                     [--confidence C] [--apply] [--from-json FILE|-] [--json]
        # Create a proposal WITHOUT an MCP server, through the same
        # validate / secret-scan / route pipeline. --from-json takes a full
        # multi-op ProposeRequest; --apply immediately lands a result that
        # would otherwise stage (you are the reviewer).

agent-memory review [STAGING_ID] [--diff] [--show] [--json] [--root DIR]
        # List staged proposals or inspect one. --diff shows a unified diff
        # of each staged file vs the current on-disk version.

agent-memory apply STAGING_ID [--json] [--root DIR]
        # Re-validate drift and apply a staged proposal.

agent-memory reject STAGING_ID [--json] [--root DIR]
        # Discard a staged proposal.

agent-memory rebase STAGING_ID [--force] [--json] [--root DIR]
        # Re-plan a staged proposal against the current disk state
        # after target_drift. --force is required for soft drifts
        # (acknowledges accepting the new base as planning input).

# review / apply / reject / rebase accept a full STAGING_ID, any unique
# prefix (Git-style), or --latest for the most recently staged proposal:
#   agent-memory apply 20260527       # unique prefix
#   agent-memory apply --latest       # newest staged proposal

agent-memory install <adapter> [--user-global] [--force] [--json]
        # Materialise agent-runtime adapter assets.
        # Supported: claude, cursor, agents, gemini.

agent-memory rebuild-index [--root DIR] [--clobber] [--no-assign-ids] [--json]
        # Recreate the FTS5 shadow index from canonical Markdown files.
        # Use for SQLite corruption, schema changes, or after manual .md edits.

agent-memory sweep [--root DIR] [--ttl DURATION] [--dry-run] [--json]
        # Remove staged proposals past the manifest's staging.ttl_seconds.
        # Each removal also writes a ttl_expired entry to meta/rejection-log.jsonl.

agent-memory version
        # Print binary version and exit.
```

## MCP tools

Exposed by `agent-memory mcp` over stdio JSON-RPC:

| Tool | Purpose |
|------|---------|
| `memory.fetch_context` | Read a budgeted Markdown context pack. |
| `memory.propose_update` | Submit structured edits (apply or stage). |
| `memory.status` | Report memory health: file counts, staged proposals (with drift), security/git/lock posture. |

## Agent-runtime adapters

`agent-memory install <adapter>` drops a worked instruction file at the
location each runtime reads from:

| Adapter | Target file | Notes |
|---------|------------|-------|
| `claude` | `.claude/skills/agent-memory/SKILL.md` | Claude Code skill format. `--user-global` writes to `~/.claude/skills/`. |
| `cursor` | `.cursor/rules/agent-memory.mdc` | Cursor MDC rule with description-based matching. `--user-global` writes to `~/.cursor/rules/`. |
| `agents` | `AGENTS.md` (repo root) | Industry-broad convention. Read by OpenAI Codex CLI, Cursor's agent mode, Sourcegraph Cody, etc. Project-local only. |
| `gemini` | `GEMINI.md` (repo root) | Gemini CLI long-term project context. Project-local only. |

Each file teaches the runtime when to call `memory.fetch_context` and
`memory.propose_update`, the intent vocabulary, provenance rules, and
debugging reject reasons. The same behavioural model across all four;
each adapter just wraps it in the runtime's native format.

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

## Releases

Tag-driven via [goreleaser](https://goreleaser.com/). Pushing a `v*`
tag triggers
[`.github/workflows/release.yml`](.github/workflows/release.yml),
which builds the binary matrix and publishes a GitHub Release with
archives attached.

Matrix per release:

- `linux_amd64`, `linux_arm64`
- `darwin_amd64`, `darwin_arm64`
- `windows_amd64`, `windows_arm64`

Each archive contains the `agent-memory` binary, `README.md`, and
`CHANGELOG.md`. A sibling `agent-memory_<version>_checksums.txt`
provides SHA-256 hashes.

```bash
# Verify a downloaded archive
sha256sum -c agent-memory_0.2.0_checksums.txt
```

Local dry-run of the release pipeline (requires `goreleaser`
installed):

```bash
goreleaser check                       # parse + validate .goreleaser.yml
goreleaser release --snapshot --clean  # full build with no upload
```

Source builds always identify as `dev`:

```
$ go build -o agent-memory ./cmd/agent-memory
$ ./agent-memory version
dev
```

Release builds via goreleaser stamp the actual tag through
`-ldflags='-X .../cli.ProgramVersion=v0.X.Y'`.

## License

[Apache License 2.0](LICENSE). You may use, modify, and distribute this
software under its terms; it includes an express patent grant. Contributions
are accepted under the same license (see [CONTRIBUTING.md](CONTRIBUTING.md)).
