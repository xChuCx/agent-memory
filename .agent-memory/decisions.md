# Decisions
<!-- @id: decisions -->

Durable architecture and product decisions, newest first. Each section
carries Date / Status / Confidence (enforced by the decisions schema).

## Use the official MCP Go SDK
<!-- @id: official-mcp-sdk -->

**Date:** 2026-05-29
**Status:** active
**Confidence:** confirmed

**Context:** Several third-party MCP libraries exist (`metoro-io`, etc.).
**Decision:** Depend on `github.com/modelcontextprotocol/go-sdk/mcp`.
**Consequences:** Tools are registered via `mcp.AddTool` with typed
input/output structs; the stdio transport is `mcp.StdioTransport`. Spike
S2 validated the SDK before adoption.
**Sources:** spikes/s2-mcp-sdk, docs/patterns/mcp-tool-server.md

## CGo-free SQLite for the shadow index
<!-- @id: cgo-free-sqlite -->

**Date:** 2026-05-29
**Status:** active
**Confidence:** confirmed

**Context:** The FTS5 shadow index needs SQLite, but cgo breaks static
cross-compilation and complicates goreleaser builds.
**Decision:** Use `modernc.org/sqlite` (pure-Go) instead of the cgo
`mattn/go-sqlite3`.
**Consequences:** Fully static binaries; no C toolchain in CI/release.
FTS5 is available. Spike S4 validated incremental UPSERT performance.
**Sources:** spikes/s4-fts5-incremental, internal/index/sqlite.go

## Writes are MCP-only; humans manage via CLI
<!-- @id: mcp-only-writes -->

**Date:** 2026-05-29
**Status:** active
**Confidence:** confirmed

**Context:** Who is allowed to mutate memory, and how?
**Decision:** The only write path is the `memory.propose_update` MCP tool.
There is deliberately NO `propose`/`update` CLI command. Humans drive the
lifecycle with `review` / `apply` / `reject` / `rebase` / `sweep`.
**Consequences:** Agents propose structured edits; durable changes are
staged for human review. Initial bootstrap content may be hand-authored
directly into the files (as this store was).
**Sources:** internal/mcp/propose.go, internal/cli/root.go

## Server decides apply-vs-stage per category
<!-- @id: server-decides-apply-vs-stage -->

**Date:** 2026-05-29
**Status:** active
**Confidence:** confirmed

**Context:** The agent should not choose dry-run vs apply.
**Decision:** Routing (`internal/memory/routing.go`) maps intent+category
to apply or stage using manifest approval policy. `create_file
if_exists=replace` force-stages on durable (git-tracked) categories only;
ephemeral local categories (current/sessions) keep auto-apply.
**Consequences:** Decisions/conventions/modules/archive stage by default;
pitfalls-append and local current auto-apply.
**Sources:** internal/memory/routing.go, internal/memory/update.go

## Optional ExtraFileProducer for multi-file ops
<!-- @id: extrafile-producer-multifile -->

**Date:** 2026-05-29
**Status:** active
**Confidence:** confirmed

**Context:** M4 archival ops (`archive_section`, `remove_section`) must
write two files: the mutated source and a new archive file.
**Decision:** Add an optional `ExtraFileProducer` interface rather than
changing the 5 existing single-file operations.
**Consequences:** Single-file ops are untouched; archive/remove implement
the interface; the orchestrator runs an extras pass. Archive files are
write-once.
**Sources:** internal/memory/operations.go, docs/patterns/archival-operations.md

## Deterministic index.md (no wall-clock)
<!-- @id: deterministic-index -->

**Date:** 2026-05-29
**Status:** active
**Confidence:** confirmed

**Context:** `index.md` is server-managed and regenerated on every durable
write; a timestamp in the body would cause git churn and flaky tests.
**Decision:** `RegenerateIndex` builds a deterministic body (tallies, no
timestamp) and writes only when content actually differs.
**Consequences:** Idempotent regeneration; stable tests; clean diffs.
**Sources:** internal/memory/index_gen.go, docs/patterns/index-regeneration.md
