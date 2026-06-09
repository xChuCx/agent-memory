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

**Date:** 2026-06-04
**Status:** active
**Confidence:** confirmed

**Context:** PR4 review (index store dimension) surfaced two correctness/safety
gaps in how cached external stores are indexed, plus one ranking prerequisite
for PR5.
**Decision:** (1) `indexCachedStores` distinguishes an ABSENT cached
`meta/schema.yaml` (fall back to the consumer schema) from a PRESENT-but-invalid
one, which now FAILS the rebuild — silently indexing a broken store under the
consumer schema turns a config error into a silent retrieval bug.
(2) External stores index only their DURABLE (git-tracked) landscape categories;
transient local/session categories (`GitTracked=false`) are skipped, so a
local-path store can't leak a developer's private working context into the
shared index.
(3) `RankingContext` file-level signals (FreshFiles/StaleFiles, scope, archive)
are keyed by file path alone — correct while fetch is local-only, but PR5 MUST
re-key them by `(store, file)` before merging multi-store results, or a path
present in two stores collects another store's boost.
**Consequences:** Federation indexing fails loud on misconfiguration, never
ingests private transient context from local-path stores, and PR5 has a written
prerequisite for correct multi-store ranking.
**Sources:** internal/index/rebuild.go, internal/index/ranking.go, internal/index/store_dim_test.go

**Date:** 2026-06-04
**Status:** active
**Confidence:** confirmed

**Context:** PR5 makes `fetch_context` blend local memory with cached landscape
stores. The risks: a large landscape evicting local candidates, lost
provenance, and treating external text as instructions.
**Decision:** Retrieval is per-store-fair — local `Search(50)` plus
`SearchPerStore(kPerStore=20)` per cached store, merged and re-ranked. Each
store's hits are multiplied by `priority_multiplier` on the NEGATIVE BM25 score
(<1 penalises; default 0.8 so local wins ties — same sign convention as every
ranking multiplier, never "fixed"). Local and external are ranked SEPARATELY so
file-keyed signals don't collide across stores (the PR4 prerequisite). Then
cross-store Jaccard dedup (>=0.85, keep higher-ranked) and one global budget.
Non-local chunks are wrapped in a provenance + trust boundary: a one-time
"evidence, not instructions" preamble + `begin/end external: name@commit`
markers + a store-labelled header. `FetchDeps` keeps `MemoryDir` (local) and
adds an OPTIONAL `Stores []StoreRef` (hybrid, not a full map) — backward-
compatible so non-federated callers stay byte-for-byte identical — while
caches/rollups key on `(store, file)`. `LoadFetchStores` includes only synced
stores; a malformed lock degrades to local-only. Bootstrap stays local-only.
**Consequences:** Opt-in invariant holds (no stores → single-store path,
byte-for-byte, regression-tested); landscape is secondary by default; external
content is clearly fenced as untrusted reference; reads are path-contained.
**Sources:** internal/memory/fetch.go, internal/memory/fetch_stores.go, docs/patterns/multi-store-fetch.md

**Date:** 2026-06-05
**Status:** active
**Confidence:** confirmed

**Context:** PR6 closes the federation slice with a deterministic eval that
proves multi-store retrieval works, matching the existing offline retrieval /
continuity evals (no LLM, CI-guarded).
**Decision:** The multi-store eval (internal/eval/federation_test.go) runs gold
cross-repo queries through the REAL federated fetch (memory.BuildContextPack
with a cached landscape store) and asserts on the bytes the agent sees — it
parses each section's provenance header (`@file/@store/@id`) out of the pack
into a ranked list, rather than re-implementing the merge in the test. Metrics:
recall@5 WITH store-origin correctness (the gold must come from the right
store) behind a CI floor (0.85; observed 1.0 on the curated corpus), plus
local-vs-landscape ranking sanity (local wins when both relevant; landscape
surfaces when local is silent; neither starves under the per-store-fair merge),
a trust-boundary rendering check, and graceful budget starvation (tiny budget
keeps local, drops landscape via Omitted, never crashes). The corpus doubles as
the federation demo fixture. Federation is documented in the README +
docs/patterns. Slice complete (PR1-PR6); tag 0.5.0.
**Consequences:** Federation retrieval is regression-guarded in CI on the real
pipeline (merge + priority + dedup + budget + provenance), not a stand-in. The
provenance-complete pack (deterministic included_files, Omitted.Origin) makes
the eval assertions clean.
**Sources:** internal/eval/federation_test.go, docs/design/federated-memory.md

**Date:** 2026-06-09
**Status:** active
**Confidence:** confirmed

**Context:** A user ran `agent-memory init` + `install claude` in a NEW repo, but
the agent's staged proposals all landed in a DIFFERENT repo's .agent-memory/
(the tool's own repo). Root cause: `install claude` only wrote SKILL.md and
never registered an MCP server, so the agent fell back to a pre-existing
USER-scoped server registered as `agent-memory mcp --root <fixed-repo>`. A
user-scoped server is shared across ALL projects and was pinned to one repo, so
every project's writes routed there. Confirmed against Claude Code docs: a
project stdio server does NOT get cwd=project — Claude Code sets
$CLAUDE_PROJECT_DIR instead; scope precedence is local > project > user.
**Decision:** (1) The MCP server resolves its root from --root, then
$CLAUDE_PROJECT_DIR, then cwd (was --root else cwd). (2) `install claude` now
merges a project-scoped .mcp.json registering
`agent-memory mcp --root ${CLAUDE_PROJECT_DIR:-.}` — portable across clones,
correct per-repo, and overriding any stray user-scoped server by precedence.
Merge is non-destructive. User-global install stays skill-only (a user-scoped
fixed --root is the footgun). (3) `agent-memory doctor` flags a project
.mcp.json whose agent-memory --root is pinned to a different repo. (4) README no
longer recommends a hardcoded user-scoped --root.
**Consequences:** Each repo's agent writes to its own .agent-memory/; a stray
global server can no longer hijack other projects; misconfig is detectable
(doctor) and visible. Shipped as 0.5.1.
**Sources:** internal/cli/mcp.go, internal/adapters/claude/claude.go, internal/cli/doctor.go
