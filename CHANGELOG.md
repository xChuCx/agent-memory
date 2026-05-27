# Changelog

All notable changes to **agent-memory** are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-05-27

First public-shape release. Implements the Core Contract from
[Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md) and Release 0.1
of the [Implementation Plan](agent-memory-implementation-plan.md): a
local context middleware that AI coding agents can call over MCP to
read project memory and write durable knowledge back, with structured
operations, drift-checked staging, secret scanning, and a worked
Claude Code adapter.

### Added

**CLI** (`agent-memory <subcommand>`):

- `init` — scaffold `.agent-memory/` (manifest, schema, conventions,
  decisions, pitfalls, index, modules/, archive/, local/, sessions/,
  staging/, meta/).
- `status [--json]` — show project state: version, memory dir, per-
  category file counts, last-known lock metadata.
- `doctor` — diagnostic layout checks; advisory output.
- `fetch [QUERY] [--scope] [--budget] [--exclude-archive] [--json]` —
  return a budgeted Markdown context pack. Empty query returns the
  bootstrap pack (branch-local current + shared current + conventions +
  index summary); non-empty query runs FTS5 + ranking.
- `mcp` — start the stdio MCP server (JSON-RPC over stdin/stdout).
- `review [STAGING_ID] [--show] [--json]` — list staged proposals
  or inspect one (with optional staged-bytes dump).
- `apply STAGING_ID [--json]` — re-validate drift, write atomically,
  re-index, remove staging dir.
- `reject STAGING_ID [--json]` — discard a staged proposal.
- `install <adapter> [--user-global] [--force] [--json]` — materialise
  agent-runtime adapter assets. `claude` adapter writes `.claude/skills/
  agent-memory/SKILL.md`.
- `version` — print binary version.

**MCP tools** (over stdio JSON-RPC, via
`github.com/modelcontextprotocol/go-sdk`):

- `memory.fetch_context` — read a budgeted Markdown pack with optional
  query / scope / budget / exclude-archive flags.
- `memory.propose_update` — submit structured edits. Validated,
  schema-checked, secret-scanned, provenance-checked, and routed to
  apply or stage based on intent + category.

**Memory model & engine**:

- **Byte-preserving Markdown engine** (`internal/markdown/`) — parse
  the goldmark AST only to locate byte offsets, then splice the
  original source. Unchanged regions are byte-identical pre/post.
- **Anchor-ID convention** (`<!-- @id: kebab-case -->` on the line
  after a heading, with at most one blank line of slack) — section
  identity decoupled from heading text.
- **Section-level splice ops** — `create_file`, `replace_section`,
  `append_section` (with first-child-slot semantic under a parent),
  `append_to_section`, `replace_section_content`.
- **FTS5 incremental index** (`internal/index/`) — SQLite FTS5 shadow
  index over section bodies. WAL + synchronous=NORMAL via URI pragmas.
  Incremental upsert on every applied write; auto-rebuilds on first
  fetch if empty.
- **Ranking signals** — scope boost, archive penalty, stale penalty,
  fresh boost, applied multiplicatively to BM25 scores.
- **Branch-aware local state** — `local/current.<slug(branch)>.md`
  resolved via shell-out to `git rev-parse`. Falls back to
  `local/current.shared.md` outside a git repo or on detached HEAD.

**Configuration**:

- `meta/manifest.yaml` — budgets, approval routing, staging TTL,
  security flags, git policy, lock timeout. Loader applies defaults
  then merges overrides; per-OS path-safety guarantees.
- `meta/schema.yaml` — per-category file/glob, section-schema rules,
  approval policy, provenance policy. Custom two-step merge for the
  `categories: {…}` map (yaml.v3 doesn't merge into map values).
- `path.Match` (not `path/filepath.Match`) for globs — `*` never
  spans `/` on any OS.

**Security model**:

- **Secret scanner** — regex set for canonical token shapes (AWS,
  GitHub, GitLab, Anthropic, OpenAI, Stripe, JWT, PEM/SSH private key
  blocks) plus Shannon-entropy fallback at threshold 4.5 / min-length
  32. `Finding` carries `Type` + `Line` only; the matched bytes never
  leave the scanner's stack frame.
- **Allowlist markers** — `<!-- @secret-scan: allow reason="..." -->`
  / `<!-- @secret-scan: end -->` pairs. `reason=` is mandatory and
  non-empty. No global disable flag — per-region only.
- **Provenance validator** — per-category `Required`,
  `RequiredForNewSections`, `AllowedSourceTypes`, `ForbiddenSourceTypes`.
  `external` and `inference` are forbidden for `record_decision` by
  default.

**Staging lifecycle**:

- `proposal.json` + `target-checksums.json` + `files/<rel-path>`
  written under `.agent-memory/staging/<id>/` when a proposal routes
  to stage. Staging ID = `<UTC YYYYMMDDTHHMMSS>-<slug(intent+rationale)>`.
- **Drift re-check on apply** per `DriftPolicy`:
  `require_section_content_match` (hash), `require_section_resolvable`
  (ID), `require_file_absent` / `require_file_present` (stat).
- Apply is atomic per file + re-indexes touched sections + removes
  the staging directory. Apply rejection (drift) leaves the staging
  directory intact for re-staging.

**Concurrency**:

- Cross-process advisory lock via `github.com/gofrs/flock` on
  `meta/lock`. No application-level TTL — the kernel releases on
  process death. Owner metadata written to a sidecar `meta/lock.info`
  (best-effort, never gates correctness).
- `WaitTimeout` configurable via `manifest.concurrency.wait_timeout_seconds`.

**Adapters**:

- **Claude Code** (`internal/adapters/claude/SKILL.md`) — embedded
  worked skill that teaches the agent when and how to call the two
  MCP tools: bootstrap fetch_context at session start, intent →
  situation table, operation kinds, provenance rules, hard limits (no
  secrets, no speculation as decision), three concrete JSON examples,
  reject-reason debugging table.

**Testing & CI**:

- ~400 unit tests across `internal/*` and `spikes/*`.
- `internal/e2e/release01_smoke_test.go` (build-tagged `e2e`) drives
  the full user flow through the compiled binary including a real MCP
  client session via the SDK's `CommandTransport`.
- GitHub Actions: test matrix on Linux/Windows/macOS, race detector
  on Linux, e2e job on Linux, golangci-lint (compiled from source via
  `install-mode: goinstall` to match the project's Go 1.25 toolchain).
- `.gitattributes` forces LF on every OS so byte-comparison fixtures
  stay deterministic.

**Documentation**:

- [Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan](agent-memory-implementation-plan.md).
- Pattern docs (`docs/patterns/`): atomic-writes, byte-preserving-engine,
  configuration-loading, cross-process-locking, mcp-tool-server,
  security-layer, propose-update-pipeline, staging-lifecycle,
  adapter-installation, e2e-smoke.
- Spike outcome docs (`docs/spikes/`) for S1-S4.

### Limitations / deferred work

Tracked for **Release 0.2 / 0.3**:

- **M4 — Git integration**: auto-stage / auto-commit on apply (via
  `manifest.git.track_local` / `track_sessions` / `auto_stage_changes`)
  is not yet implemented. Manual `git add` of applied files is needed
  if the project tracks them.
- **M5 batch 2 — Staging TTL sweeper**: `manifest.staging.ttl_seconds`
  is parsed but not enforced. Old staged proposals accumulate until
  manually rejected.
- **M5 batch 2 — Rejection audit log**: discarded proposals leave no
  trace beyond the directory being gone.
- **M7 — `rebuild-index` / `rebase` commands**: index repair must be
  done by deleting `meta/index.sqlite*` and re-running fetch (which
  auto-rebuilds).
- **M7 — Git merge driver**: documented in manifest but `init
  --with-merge-driver` is currently a no-op.
- **M8 — Benchmark / eval harness**: `internal/e2e/` has a latency
  regression guard but no formal bench scaffold.
- **Multi-runtime adapters**: only Claude Code ships in 0.1. Cursor,
  Codex, Gemini, etc. land in 0.2.
- **MCP server registration**: `install claude` writes the SKILL.md
  but does not edit `~/.claude/mcp_servers.json`. Users still
  configure the MCP server entry manually.

### Threat model recap

What 0.1 does NOT defend against:

- A malicious agent that crafts an allowlist marker around a real
  credential and bypasses the scanner. Allowlist regions are
  intentionally trusted; the policy is "use it for token-format docs,
  not real secrets".
- A user with write access to `.agent-memory/` who edits files
  manually. The byte-preserving engine, atomic writes, and FTS
  re-index keep things consistent; manual edits race the orchestrator.
- Anything outside `.agent-memory/`. Path validation refuses writes
  that escape root, but the binary trusts whatever the host filesystem
  reports.

### Migration / upgrade

This is the first release; no migration path required. The on-disk
schema (`meta/schema.yaml` version `0.4.1`) is the baseline.

[0.1.0]: https://github.com/xChuCx/agent-memory/releases/tag/v0.1.0
