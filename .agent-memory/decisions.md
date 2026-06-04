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

**Date:** 2026-06-04
**Status:** active
**Confidence:** confirmed

Deliver system-level ("landscape") memory as PR1–PR6, branch-per-PR, behind an
opt-in invariant: with no `stores` declared in the manifest, behavior is
byte-for-byte the single-store path. Contract choices: a monotonic
`store_format_version` in manifest.yaml (fail-closed on a too-new store);
referenced stores use the `stores` / `revision` / `priority_multiplier`
vocabulary (priority is a multiplier on the negative-BM25 score, so <1
penalizes); landscape stores are read-only from a consuming repo in slice 1;
synced stores are materialised into a sanitized cache (symlink/path-escape
rejected) and treated as untrusted context, not instructions. Full design:
docs/design/federated-memory.md.

**Date:** 2026-06-04
**Status:** active
**Confidence:** confirmed

`agent-memory sync` (PR3) materialises each referenced store into the gitignored
cache and pins it in stores.lock. Pipeline per store: clone+checkout (or
local-path copy → `unlocked`) into a temp dir → fs.CopyDirValidated (reject and
never-follow symlinks, contain paths, regular files only) → secret/PII scan on
ingest using the CONSUMER's security settings (external allowlist markers do NOT
self-exempt) → fs.SwapDir atomic swap into meta/cache/stores/<name> (Windows-safe;
no half-synced cache ever visible) → record the resolved commit. Failed stores are
reported and skipped; removed stores are reconciled out of lock + cache. No
context/index changes yet (PR4/PR5). See docs/design/federated-memory.md §6.2, §7.

**Date:** 2026-06-04
**Status:** active
**Confidence:** confirmed

**Context:** Federation (PR4) needed one shadow index to hold the local memory
plus cached landscape stores. FTS5's column set and composite primary keys
can't be ALTERed in place, and the index is a derived, rebuildable cache.
**Decision:** Add a `store` column to all three index tables — UNINDEXED and
last in `memory_search` so `MATCH` relevance and the positional `snippet()`
index are unchanged; composite keys `(store,file,section_id)` / `(store,file)`.
Bump `SchemaVersion` 1->2 and migrate by rebuild-on-version-bump: `Init`
drops+recreates, and every index-opening path self-heals an empty index (the
`propose`/`apply` write paths gained the read paths' rebuild-if-empty guard).
The legacy `Search`/`Get*`/`List*` stay scoped to the reserved `local` store so
fetch and status are byte-for-byte unchanged; `SearchPerStore(query,kPerStore,
stores)` does per-store-fair top-K retrieval for PR5. `RebuildAll` indexes the
local tree (skipping `meta/cache/`) then each cached store under its name.
**Consequences:** The opt-in invariant holds (no cache dir -> identical
behavior); cached-store rows never collide with local rows; no fragile in-place
migrations to maintain.
**Sources:** internal/index/{sqlite,query,rebuild,incremental}.go, docs/patterns/sqlite-fts5-shadow-index.md
