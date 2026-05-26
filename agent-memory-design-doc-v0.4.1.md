# Agent Memory Protocol — Design Doc v0.4.1

**Status:** Draft v0.4.1
**Primary implementation language:** Go
**MCP SDK:** `github.com/modelcontextprotocol/go-sdk`
**Storage model:** Markdown source of truth + incremental SQLite FTS5/BM25 shadow index
**Target users:** developers and AI coding agents working with large or long-lived codebases
**Working name:** `agent-memory`

---

## 0. Change Summary

### v0.4.1 — Pre-Coding Polish (this revision)

Risk check before implementation revealed three concrete issues. v0.4.1 is polish, not redesign: agent-facing MCP contract, schema, byte-preserving engine, per-branch local state, three-tool surface, and milestone plan are unchanged.

1. **Concurrency uses OS-level advisory locks only.**
   v0.4 mixed two incompatible mechanisms: a PID-file-with-TTL design in §11 and `gofrs/flock` (OS-level locks) in §27.2. They contradict. v0.4.1 chooses OS-level locks exclusively (`flock` on POSIX, `LockFileEx` on Windows). The kernel handles process death automatically — no TTL logic, no stale-recovery code, no race conditions on cleanup. The lock file's contents become purely informational metadata for `status` debugging. See §11.

2. **Drift detection is per-section, not per-file.**
   v0.4 checksummed entire files in `target-checksums.json`. An unrelated edit (e.g., a comma added to section X while a proposal targets section Y in the same file) would falsely trigger `target_drift` on apply. v0.4.1 checksums per-operation-target. Section IDs are resolved at apply time against current file state; only the *targeted* section's content hash must match. Append-style operations use a weaker policy (target must still resolve, content may have grown). Byte-offset shifts from unrelated edits no longer matter. See §16.3 and §16.4.

3. **CLI accepts staging-ID prefixes.**
   Typing `agent-memory apply 2026-05-26T121500-auth-token-rotation` is hostile. v0.4.1 accepts any unique prefix (Git-style): `apply 2026` or `apply auth` works when unambiguous. Ambiguous prefixes list candidates with a hint. Adds `--latest` shortcut for the most recent staged update. Interactive TUI selector (bubbletea-based) is listed in §35 future work — not MVP. See §16.6 and §17.1.

### v0.4 — vs v0.3

This revision addresses the blocking gaps identified in the v0.3 review: concurrency, section identity, Markdown roundtrip stability, branch-aware local state, schema formality, and adapter concreteness. It also tightens the MCP surface, gives staging a quality-of-life pass, and adds explicit positioning and evaluation chapters.

### Breaking changes from v0.3

1. **Three MCP tools, not four.**
   `memory.record_session` is folded into `memory.propose_update` via `intent: "session_log"`. Sessions remain local-ephemeral; the agent does not need a separate primitive, and the surface stays smaller.

2. **Stable section IDs are now the primary key.**
   `replace_section`, `append_to_section`, `remove_section`, and `archive_section` locate sections by an opaque ID anchor placed immediately after the heading:

   ```md
   ## Token Rotation
   <!-- @id: token-rotation -->
   ```

   Heading text is a hint for new sections and a fallback when no ID exists. Auto-IDs are assigned on first AST traversal. Renaming a heading no longer invalidates references.

3. **Byte-preserving Markdown engine.**
   The server never round-trips a file through an AST renderer. It parses the AST to locate byte offsets of the target section, then performs a string-level splice. Unchanged regions remain byte-identical. This is a hard requirement: no whitespace churn, no list-marker rewrites, no fence-style normalization.

4. **Per-branch local state by default.**
   `current.md` is replaced with `local/current.<slug>.md`, where `<slug>` is derived from the active Git branch. On branch switch, the agent's next `fetch_context` automatically picks up the new branch's current state. A `local/current.shared.md` slot exists for cross-branch notes.

5. **Concurrency model is explicit.**
   A single-writer OS-level advisory lock on `.agent-memory/meta/lock` protects all mutating operations. Readers use the on-disk state directly without locks. The kernel releases the lock automatically on process exit. The full model is specified in §11.

6. **YAML manifest, not JSON.**
   `manifest.yaml` replaces `manifest.json`. JSON remains for machine artifacts (staged proposals, MCP tool I/O, status payloads).

7. **Differentiated default approval policy.**
   Approval requirements are per-category, not global. Defaults:
   - `decisions.md`, `conventions.md`, `archive/*`, `meta/manifest.yaml` — staged for human approval.
   - `pitfalls.md`, `modules/*.md` — staged.
   - `local/current.<branch>.md`, `sessions/*.md` — applied immediately.
   - `index.md` — server-maintained; agents cannot write directly.

8. **Section-level removal exists.**
   `remove_section` is added, with mandatory `archive_path` (cannot truly destroy content). `replace_section_content` exists for partial-section edits without touching the heading or ID.

9. **Incremental indexing from day one.**
   FTS5 incremental update per write. No full rebuild after every operation. Full rebuild only on demand via `agent-memory rebuild-index`.

10. **Schema is formal.**
    Each memory category has a JSON-Schema-compatible definition declaring required sections, metadata, provenance rules, and approval policy. The schema is human-editable and validated on every write (§25).

11. **Adapter quality is a first-class deliverable.**
    A worked SKILL.md example is part of the spec (§18.5), not deferred to implementation. The acceptance criterion for an adapter is observed agent behaviour, not file generation.

### Additions

- Concurrency & locking model (§11)
- Section identity model (§12)
- Per-branch local state model (§13)
- Byte-preserving Markdown engine spec (§21)
- Git merge strategy for durable memory (§24.5)
- Formal schema definition with examples (§25)
- Worked SKILL.md adapter example (§18.5)
- Competitive positioning (§28)
- Evaluation methodology (§29)
- Secret-scan allowlist mechanism (§23.6)

### Retained from v0.3

Core principles, three memory classes, archival philosophy, the Go + official MCP SDK + SQLite + Markdown stack, MVP milestone shape, and the agent-facing operation vocabulary (`create_file`, `append_section`, `replace_section`, `append_to_section`, `archive_section`) are unchanged in intent. They become more precise.

---

## 1. Summary

`agent-memory` is a local context middleware for AI coding agents.

It maintains a structured, human-readable Markdown memory layer for a repository and exposes it through a minimal MCP interface (three tools). The server owns the fragile work — section identity, byte-preserving Markdown edits, secret scanning, validation, atomic writes, staging — so agents can issue safe, semantic, structured updates without ever generating a patch.

```text
agent asks for context
        ↓
server searches, ranks, budgets, returns one pack
        ↓
agent works
        ↓
agent proposes structured memory operations
        ↓
server validates, scans, applies-or-stages
        ↓
memory remains small, safe, searchable, reviewable, byte-stable
```

- Source of truth: Markdown.
- Interface: MCP (stdio).
- Implementation: Go, one binary.
- Index: SQLite FTS5/BM25, incremental, ignored by Git, rebuildable.
- Section identity: opaque IDs, stable across heading renames.
- File edits: byte-preserving splice, never AST round-trip.

---

## 2. Problem

AI coding agents degrade on large or long-lived projects because they lack durable, trustworthy, budgeted project memory. The product addresses ten failure modes:

1. **Repeated rediscovery.** Agents re-read the same files to relearn conventions, architecture, test commands, module boundaries, and prior decisions.
2. **Context-window pressure.** Large repositories cannot fit; agents need a curated map, not raw bytes.
3. **Stale or bloated memory.** Naive memory-bank approaches grow forever.
4. **Unsafe writes.** Agents persist secrets, customer data, prompt-injection content, or incorrect assumptions.
5. **Tool overload.** Rich MCP servers cause agents to waste turns or loop.
6. **Bad patch generation.** Agents are unreliable at unified diffs over Markdown.
7. **False review loops.** Self-review by the same agent that generated the diff is fake safety.
8. **Git conflicts in local state.** `current.md` and session logs collide when committed by multiple developers.
9. **Cross-agent fragmentation.** Claude Code, Codex, Cursor, Cline, Windsurf, Copilot all use different instruction and memory mechanisms.
10. **Concurrent agent writes.** When two agents (or an agent and a developer) touch memory simultaneously, naive file writes corrupt state.

v0.4 also explicitly addresses:

11. **Heading fragility.** Renaming a section heading invalidates external references unless identity is decoupled from text.
12. **Markdown roundtrip churn.** AST render → string can change whitespace, list markers, and fences, producing noisy diffs that break `git blame`.
13. **Branch-aware state.** `current.md` is per-task; tasks are per-branch; memory must follow the branch.

---

## 3. Goals

### 3.1 Product Goals

- Compact, safe, searchable, branch-aware project memory.
- Cross-agent: Claude Code, Codex, Cursor, generic MCP clients.
- Tiny public MCP surface (three tools).
- Markdown as the human-readable source of truth.
- One-command initialization in any Git repository.
- Reviewable durable memory updates (staging by default for high-impact categories).
- Memory bloat prevention via archival compaction.
- Reject secrets before write.
- Never give agents fragile patch-based interfaces.
- Keep local state out of shared Git history.
- Survive concurrent agent + human edits without corruption.

### 3.2 Engineering Goals

- Ship as a single Go binary.
- Use the official Go MCP SDK.
- No runtime dependency on Node, Python, Docker, or external services.
- SQLite FTS5/BM25 with incremental updates.
- Byte-preserving Markdown edits.
- Avoid vector search in v0; revisit only with evidence.
- `stdio` MCP transport for local agent usage.
- Clean boundary between core memory logic and adapters.
- Replaceable indexing layer (Rust port path open, not required).

---

## 4. Non-Goals

For MVP, this project will not:

- Build a full vector database.
- Store entire conversations as durable memory.
- Replace code search, IDE tooling, or LSP.
- Replace `AGENTS.md`, `CLAUDE.md`, or Cursor rules — it augments them.
- Automatically trust generated memory updates.
- Use destructive compaction.
- Require a remote service or cloud sync.
- Be a personal knowledge management system.
- Solve general long-term LLM memory outside a repository.
- Ask agents to generate or apply unified diffs.
- Ask agents to approve their own changes.
- Provide a UI beyond CLI.
- Support multi-repository workspace memory (deferred to v0.5+).

---

## 5. Core Principles

1. **One fetch, not many reads.** Agents call one tool to get the right context pack.
2. **Markdown is the source of truth.** Human-readable, human-editable, Git-tracked where appropriate.
3. **SQLite is a shadow index.** Local, derived, ignored by Git, rebuildable, incrementally updated.
4. **MCP is an interface, not a storage format.** The memory format must survive client churn.
5. **Agent-facing operations are semantic, not line-based.** Agents propose section-level operations identified by stable IDs.
6. **Section identity is decoupled from heading text.** Renames don't break references.
7. **Markdown edits are byte-preserving.** Unchanged regions are never reformatted.
8. **Compaction archives, never destroys.** All "removals" copy content to `archive/` first.
9. **Approval is policy-controlled and category-aware.** Decisions/conventions/archive default to staged; local state and sessions apply immediately.
10. **Every durable fact has provenance.** Source type (file, test, user, session, inference) and confidence are required for staged categories.
11. **Secrets are rejected before write.** Allowlist mechanism for legitimate examples.
12. **Tool surface stays tiny.** Public MCP tools: three.
13. **Local state never pollutes shared Git history.** Branch-aware, gitignored by default.
14. **Concurrency is explicit, not implicit.** Single-writer advisory lock, snapshot reads, stale-lock recovery.
15. **Context is budgeted, ranked, and freshness-aware.**

---

## 6. Target Users

### 6.1 Primary

- Developers using coding agents on non-trivial repositories (>50 files, multi-module, long-lived).
- AI engineering teams building workflows around Claude Code, Codex, Cursor.
- Teams with large monorepos or long-running feature branches.

### 6.2 Secondary

- Open-source maintainers wanting agent-friendly repositories.
- Internal platform teams standardizing agent workflows.
- Agent framework authors looking for a portable project-memory layer.

### 6.3 Anti-Personas (explicit)

- Solo developers on small repos with stable context — overhead exceeds value.
- Teams refactoring continuously — memory goes stale faster than approval can validate it.
- Users wanting a general second-brain or PKM tool — out of scope.

---

## 7. High-Level Architecture

```mermaid
flowchart TD
    Agent[AI Coding Agent] --> Adapter[Agent Adapter]
    Adapter --> MCP[MCP Server]
    Agent --> MCP

    MCP --> Fetch[Context Fetch Engine]
    MCP --> Update[Structured Update Engine]
    MCP --> Status[Status Engine]

    Fetch --> Index[SQLite FTS5 Incremental Shadow Index]
    Fetch --> MemoryFiles[.agent-memory Markdown Files]

    Update --> Lock[Concurrency Lock]
    Update --> AST[Byte-Preserving Markdown Engine]
    Update --> SectionID[Section Identity Resolver]
    Update --> SecretScan[Secret Scanner + Allowlist]
    Update --> Validator[Schema + Markdown Validator]
    Update --> Staging[Staging Engine + TTL + Drift Detector]
    Update --> Archive[Archival Compaction]
    Update --> Git[Git Integration]

    Index --> MemoryFiles
    AST --> MemoryFiles
    Lock --> MemoryFiles
    Git --> MemoryFiles
```

### 7.1 Main Components

| Component | Responsibility |
|---|---|
| CLI | `init`, `fetch`, `status`, `review`, `apply`, `reject`, `install`, `mcp`, `rebuild-index`, `doctor` |
| MCP Server | Exposes three tools to agents |
| Fetch Engine | Builds ranked, budgeted context packs |
| Index Engine | SQLite FTS5 shadow index, incremental |
| Concurrency Lock | Single-writer advisory lock on `meta/lock.json` |
| Section Identity Resolver | Maps `(file, id)` or `(file, heading, occurrence)` to byte ranges |
| Byte-Preserving Markdown Engine | AST locate + string splice |
| Update Engine | Validates, applies, or stages structured operations |
| Security Engine | Secret scanner + entropy + allowlist; poisoning checks |
| Staging Engine | TTL-aware, drift-detecting human review queue |
| Archiver | Moves content to `archive/`; never destroys |
| Adapter Generator | Creates per-agent skill/rules files |
| Git Integration | Diff preview, optional commits, merge-driver advice |

---

## 8. Memory Classes

Three classes. Each has explicit Git, approval, and concurrency semantics.

### 8.1 Durable Shared Memory

Committed to Git. Staged for human approval by default. Provenance required.

```text
.agent-memory/index.md             (server-maintained, agents cannot write directly)
.agent-memory/conventions.md
.agent-memory/decisions.md
.agent-memory/pitfalls.md          (auto-apply for new entries; replace requires approval)
.agent-memory/modules/*.md
.agent-memory/archive/*.md         (write-once via archive_section; no direct edits)
.agent-memory/meta/manifest.yaml
.agent-memory/meta/schema.yaml
.agent-memory/.gitignore
```

### 8.2 Local Ephemeral Memory

Gitignored by default. Auto-applied (no staging). Branch-aware.

```text
.agent-memory/local/current.<branch-slug>.md
.agent-memory/local/current.shared.md       (optional, cross-branch)
.agent-memory/sessions/<date>.md
.agent-memory/staging/<id>/
```

### 8.3 Derived Memory

Gitignored. Rebuildable from source of truth.

```text
.agent-memory/meta/index.sqlite
.agent-memory/meta/index.sqlite-*    (WAL, SHM)
.agent-memory/meta/lock.json
.agent-memory/meta/cache/
```

---

## 9. Repository Memory Layout

```text
.agent-memory/
  index.md                          # server-maintained routing file
  conventions.md
  decisions.md
  pitfalls.md

  modules/
    auth.md
    payments.md
    frontend.md

  archive/
    2026-05-auth-cookie-v1.md

  local/
    current.feature-auth-rotation.md
    current.shared.md

  sessions/
    2026-05-26.md

  staging/
    2026-05-26T121500-auth-token-rotation/
      proposal.json
      preview.diff
      target-checksums.json         # for drift detection
      files/

  meta/
    manifest.yaml
    schema.yaml
    lock.json
    index.sqlite
    cache/
```

### 9.1 Required `.agent-memory/.gitignore`

`agent-memory init` creates:

```gitignore
# Local state
local/
sessions/
staging/

# Derived
meta/index.sqlite
meta/index.sqlite-*
meta/*.db
meta/*.db-*
meta/lock.json
meta/cache/
```

### 9.2 Git-Tracked by Default

```text
index.md
conventions.md
decisions.md
pitfalls.md
modules/
archive/
meta/manifest.yaml
meta/schema.yaml
.gitignore
```

### 9.3 Optional Tracking

A team may opt-in to share `local/` or `sessions/` via manifest:

```yaml
git:
  track_local: false
  track_sessions: false
```

This is not recommended for multi-developer repos. The default is `false`.

---

## 10. Memory Files

### 10.1 `index.md`

Server-maintained routing file. **Agents cannot write to it directly.** It is regenerated on every durable memory change, summarising file purpose, freshness, and stale areas.

Generated structure:

```md
# Agent Memory Index
<!-- @generated: do not edit by hand; use `agent-memory rebuild-index` -->

## Always include
- local/current.<branch>.md — current local task state
- conventions.md — build, test, style, workflow rules

## Topic map
- decisions.md — durable architecture/product decisions (3 active, 2 superseded)
- pitfalls.md — known traps (5 entries)
- modules/auth.md — authentication notes (fresh)
- modules/payments.md — payments module notes (stale: last confirmed 2026-03-12)

## Archive
- archive/ — 7 archived contexts, fetched only on strong match

## Freshness
Last full validation: 2026-05-26
Stale areas: modules/payments.md, modules/frontend.md
```

### 10.2 `local/current.<branch>.md`

Per-branch local state. Resolved at read time from the active Git branch (see §13). Examples of content:

- Current active task and goal.
- Last known implementation state.
- Next steps.
- Blockers.
- Open questions.
- Recently modified areas in the working tree.

Auto-applied (no staging). Gitignored by default.

### 10.3 `conventions.md`

Durable working rules. Per-section schema (see §25). Examples:

- Test commands.
- Formatting and linting.
- Branching conventions.
- Migration rules.
- Naming conventions.
- Required review practices.

Staged for human approval.

### 10.4 `decisions.md`

ADR-like decisions. Each section is one decision with required metadata.

```md
## Decision: Use refresh-token rotation
<!-- @id: refresh-token-rotation -->

Date: 2026-05-26
Status: active
Confidence: confirmed
Supersedes: cookie-refresh-v1

Context:
...

Decision:
...

Consequences:
...

Sources:
- file: internal/auth/refresh.go
- test: internal/auth/refresh_test.go
```

Staged for human approval. Provenance required.

### 10.5 `pitfalls.md`

Known traps. Bullet-level entries.

```md
## Authentication
<!-- @id: pitfalls-authentication -->

- Old tests assumed refresh tokens remained stable; rotation breaks them.
  Files: internal/auth/refresh_test.go
  Confidence: confirmed
  Recorded: 2026-05-26

- Don't cache decoded JWTs in process memory across requests.
  Files: internal/auth/jwt.go
  Confidence: inferred
  Recorded: 2026-04-12
```

Append-only at bullet level (auto-apply). Section-level replace requires approval.

### 10.6 `modules/*.md`

Module-specific durable memory. One file per module. Sections per concern (API, invariants, gotchas, references). Staged for approval.

### 10.7 `archive/*.md`

Historical context, write-once. Created by `archive_section`. Not loaded by default; only on strong relevance match. Cannot be directly edited (only appended to via `archive_section`).

### 10.8 `sessions/*.md`

Local session summaries. Raw material for curated memory updates. Auto-applied. Gitignored by default.

---

## 11. Concurrency and Locking Model

The single most under-specified area in v0.3. This section is fully prescriptive.

### 11.1 Lock File

`.agent-memory/meta/lock` — held via an OS-level advisory file lock (`flock(LOCK_EX)` on POSIX, `LockFileEx` on Windows). The file persists between runs; the lock is on the open file handle, not the file's existence.

The kernel owns lock state. When a process exits — clean exit, panic, OOM, SIGKILL, power loss — the lock is released automatically. There is no stale-lock recovery code path because there is no stale state to recover.

The file's contents are purely informational metadata, written by the current lock-holder and read back by `agent-memory status` for debugging:

```json
{
  "owner_pid": 48213,
  "owner_id": "claude-code-session-abc",
  "owner_kind": "agent",
  "acquired_at": "2026-05-26T12:15:00Z",
  "op_id": "propose_update-7c4a"
}
```

This metadata never gates correctness. If the previous owner died without overwriting it, the next acquirer overwrites on lock acquisition. Stale metadata visible between processes is harmless — the OS lock state is ground truth.

### 11.2 Operations Classified

| Operation | Lock Required | Reads Snapshot? |
|---|---|---|
| `memory.fetch_context` | No | Yes (best-effort) |
| `memory.status` | No | Yes |
| `memory.propose_update` (apply) | Yes | Yes |
| `memory.propose_update` (stage) | Short lock for staging dir only | N/A |
| `apply <staging_id>` | Yes | Yes |
| `reject <staging_id>` | Short lock for staging dir | N/A |
| `rebuild-index` | Yes | Yes |
| `archive_section` | Yes | Yes |

### 11.3 Acquisition Protocol

Implementation uses `gofrs/flock`:

1. Open or create `meta/lock` (regular file, mode 0644).
2. Attempt `TryLock` (non-blocking `LOCK_EX | LOCK_NB`).
3. If acquired:
   - Truncate the file.
   - Write current metadata JSON (owner_pid, owner_id, owner_kind, acquired_at, op_id) — best-effort, failure to write is non-fatal.
   - Perform work.
   - On completion: close the file handle. The kernel releases the lock atomically as part of the close.
4. If not acquired:
   - Optionally retry with blocking `Lock()` up to `concurrency.wait_timeout_seconds` (default 10s).
   - On timeout, return `LockHeld` error to the caller. The error includes whatever metadata the lock file currently contains (informational only).

Notes:

- No `O_CREAT | O_EXCL`. The lock file is a long-lived artifact; only the OS lock on it is transient.
- No timestamp comparison, no TTL math, no rename-to-expired dance.
- No file deletion on release. The file stays; only the lock is released.

### 11.4 Crash Recovery

There is no crash-recovery code. The OS handles it.

When a process dies (crash, OOM, SIGKILL, power loss), the kernel closes its file handles, which releases the advisory lock immediately. The next process attempting `TryLock` succeeds without any cleanup step.

The only residual artifact is potentially stale metadata in `meta/lock` (the previous owner didn't get to overwrite or clear it before dying). This is harmless:

- `agent-memory status` may briefly show misleading owner info, until the next lock-holder overwrites on acquisition.
- `agent-memory doctor` reports lock-file metadata for inspection; it does not "fix" anything because there is nothing to fix.

### 11.5 Read Isolation

Readers (`fetch_context`, `status`) do not take the lock. They read on-disk files directly. Since writes use atomic rename, readers see either pre-write or post-write state, never partial.

For consistent multi-file reads (e.g., `fetch_context` reading 5 module files), the read engine snapshots paths and checksums; if a write happens mid-read, it logs the inconsistency and may re-read. This is best-effort, not strict; the underlying invariant is that no individual file is ever read in a torn state.

### 11.6 Staging vs Apply

`propose_update` that results in a staged proposal needs only a short lock to create the staging directory (which has its own unique timestamp ID — no contention).

`apply <staging_id>` needs the main lock because it mutates durable files.

### 11.7 Cross-Process Coordination

Two agents on the same repo (e.g., Claude Code + Cursor) share `lock.json`. Conflicting writes serialize. Each MCP server instance is one writer process.

If a developer is editing memory in their IDE while an agent applies a change, no lock prevents this — but `apply` includes a drift check (checksums of target files match the proposal's `target-checksums.json`). On drift, apply fails with `target_drift`. The agent re-fetches and re-proposes.

### 11.8 No Distributed Coordination

In v0.4, concurrency is single-host. Remote MCP and distributed coordination are out of scope.

---

## 12. Section Identity Model

The single largest source of fragility in v0.3 was heading-based addressing. v0.4 fixes this with stable section IDs.

### 12.1 Anchor Format

Immediately after a heading line, the server places an HTML comment:

```md
## Token Rotation
<!-- @id: token-rotation -->
```

The ID:

- Is lowercase kebab-case.
- Is unique within its file.
- Is immutable across heading renames.
- Survives section moves within the same file.
- Is auto-generated on first AST traversal if absent.
- Is preserved by the byte-preserving engine (it's just text in the file).

### 12.2 Auto-Assignment Algorithm

When the server first sees a heading without an `@id` anchor:

1. Slug the heading text (lowercase, alphanumeric + dash).
2. If slug exists in this file, append `-2`, `-3`, etc.
3. Write the anchor line immediately after the heading.
4. This is the only "AST round-trip" write the engine ever performs, and it's idempotent — once IDs exist, the engine never rewrites them.

### 12.3 Agent-Facing API

Operations accept either:

```json
{
  "section_id": "token-rotation"
}
```

or, when creating a new section or working with un-anchored files:

```json
{
  "heading": "Token Rotation",
  "heading_level": 2,
  "occurrence": 1
}
```

Server resolution order:

1. If `section_id` provided → resolve by ID. Fail if not found.
2. Else → resolve by `(heading, heading_level, occurrence)`. Fail on ambiguity.

### 12.4 Heading Rename Workflow

If an agent wants to rename a heading:

```json
{
  "operation": "rename_heading",
  "path": "modules/auth.md",
  "section_id": "token-rotation",
  "new_heading": "Refresh Token Rotation"
}
```

The ID stays the same. The heading text changes. All other references remain valid.

### 12.5 ID Migration

`agent-memory rebuild-index` includes an "assign-missing-ids" pass that adds anchors to any heading lacking one. This runs once on `init` after the first index build, and on demand.

### 12.6 Index Schema

The SQLite index keys by `(file, section_id)`, not by heading text:

```sql
CREATE TABLE memory_sections (
  file TEXT NOT NULL,
  section_id TEXT NOT NULL,
  heading TEXT NOT NULL,
  heading_level INTEGER NOT NULL,
  byte_start INTEGER NOT NULL,
  byte_end INTEGER NOT NULL,
  content_hash TEXT NOT NULL,
  PRIMARY KEY (file, section_id)
);
```

---

## 13. Per-Branch Local State

### 13.1 Path Resolution

The active branch slug determines the path:

```text
local/current.<slug(branch)>.md
```

Where `slug(branch)` lowercases and replaces non-alphanumerics with `-`. E.g.:

- `main` → `local/current.main.md`
- `feature/auth-rotation` → `local/current.feature-auth-rotation.md`
- `bugfix/JIRA-123` → `local/current.bugfix-jira-123.md`

### 13.2 Resolution Algorithm

At every read/write to "current":

1. Run `git rev-parse --abbrev-ref HEAD`.
2. If Git is available and a branch is checked out, use `local/current.<slug>.md`.
3. If detached HEAD, use `local/current.detached-<short-sha>.md`.
4. If not a Git repo, fall back to `local/current.md`.

### 13.3 Shared Cross-Branch Notes

`local/current.shared.md` exists for state that survives branch switches (e.g., "currently investigating intermittent test failure in CI, suspected cause: timezone"). Always read in addition to the branch-specific file.

The bootstrap context concatenates:

```text
local/current.<branch>.md
local/current.shared.md
conventions.md
index.md summary
```

### 13.4 Cleanup

Old `local/current.*.md` files accumulate as branches come and go. `agent-memory doctor` reports orphans (current files for branches no longer present). `agent-memory clean-local --dry-run` and `agent-memory clean-local --apply` prune them.

### 13.5 Worktrees

Git worktrees each have their own `.agent-memory/local/` (since worktrees have separate working directories, this is automatic).

### 13.6 Per-Branch Sessions

Sessions are not per-branch by default. The session file name is dated, not branched. If a team wants per-branch sessions, manifest can set `sessions.per_branch: true`, but this is opt-in.

---

## 14. Public MCP Tool Surface

The MCP server exposes **three** tools:

```text
memory.fetch_context
memory.propose_update
memory.status
```

`memory.record_session` from v0.3 is removed. Sessions are written via `propose_update` with `intent: "session_log"` — the server routes to `sessions/` based on intent, not a separate tool.

Internal operations (not exposed): `read_file`, `search`, `apply_patch`, `compact`, `delete_section`, `rename_heading` (used as sub-operation only).

---

## 15. MCP Tool Contracts

### 15.1 `memory.fetch_context`

Fetches a compact, ranked, budgeted context pack.

#### Input

```json
{
  "query": "auth refresh token migration",
  "scope": ["auth"],
  "budget": 6000,
  "include": ["current", "conventions", "decisions", "pitfalls", "modules"],
  "exclude_archive": true
}
```

| Field | Type | Required | Description |
|---|---:|---:|---|
| `query` | string | no | Search query. If empty, returns bootstrap context. |
| `scope` | string[] | no | Paths or module names to prioritize. |
| `budget` | number | no | Approximate output budget in characters (default from manifest). |
| `include` | string[] | no | Context categories to include. |
| `exclude_archive` | boolean | no | If true (default), archive is never returned unless query strongly matches. |

#### Output

```json
{
  "context": "...markdown context pack...",
  "included_files": [
    {
      "path": ".agent-memory/local/current.feature-auth-rotation.md",
      "reason": "bootstrap: active branch local state",
      "freshness": "fresh",
      "confidence": "user-provided",
      "section_count": 1
    }
  ],
  "omitted": [
    {
      "path": ".agent-memory/archive/2026-05-auth-cookie-v1.md",
      "reason": "archived; below relevance threshold (score 0.31)"
    }
  ],
  "suggested_next_queries": [
    "auth token tests",
    "refresh service pitfalls"
  ],
  "context_metadata": {
    "active_branch": "feature/auth-rotation",
    "budget_used": 5840,
    "budget_remaining": 160,
    "stale_warnings": ["modules/payments.md last confirmed 2026-03-12"]
  }
}
```

#### Behavior

Empty `query`:

```text
return bootstrap pack:
  local/current.<branch>.md + local/current.shared.md
  + conventions.md
  + compact index.md summary
```

Non-empty `query`:

```text
1. Always include current.<branch>.md and current.shared.md if present.
2. Include conventions.md unless explicitly excluded.
3. Search via SQLite FTS5 (BM25 ranking).
4. Boost scope matches (×2.0), fresh files (×1.5), active branch references (×1.3).
5. Penalize archive (×0.4) unless score still beats threshold.
6. Penalize stale-marked files (×0.6).
7. Chunk long files by section.
8. Deduplicate semantically overlapping sections (Jaccard on tokens > 0.85 → skip duplicate).
9. Enforce budget greedily by descending score.
10. Render one Markdown context pack with section headers preserved.
11. Return provenance and stale warnings.
```

### 15.2 `memory.propose_update`

The only agent-facing write tool.

The agent does not submit unified diffs. The agent does not choose `dry_run` or `apply`. The server decides apply-vs-stage based on per-category policy.

#### Input

```json
{
  "intent": "refresh_module",
  "rationale": "Auth refresh flow changed after token rotation refactor.",
  "operations": [
    {
      "operation": "replace_section",
      "path": "modules/auth.md",
      "section_id": "token-rotation",
      "content": "## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens are rotated on every successful use.\n\nSources:\n- file: internal/auth/refresh.go\n- test: internal/auth/refresh_test.go\n"
    }
  ],
  "changed_files": [
    "internal/auth/refresh.go",
    "internal/auth/refresh_test.go"
  ],
  "sources": [
    {"type": "file", "ref": "internal/auth/refresh.go"},
    {"type": "test", "ref": "internal/auth/refresh_test.go"}
  ],
  "confidence": "confirmed"
}
```

| Field | Type | Required | Description |
|---|---:|---:|---|
| `intent` | enum | yes | Categorical intent (drives routing and approval policy). |
| `rationale` | string | yes | Why this update is needed. |
| `operations` | object[] | yes | Structured Markdown operations (1+). |
| `changed_files` | string[] | no | Repository files motivating the update. |
| `sources` | object[] | conditionally | Required for categories where schema demands provenance. |
| `confidence` | enum | no | `confirmed`, `inferred`, `user-provided`. Default `inferred`. |

#### Supported Intents

```text
update_current        → routes to local/current.<branch>.md, auto-apply
update_shared         → routes to local/current.shared.md, auto-apply
session_log           → routes to sessions/<date>.md, auto-apply (replaces v0.3 record_session)
add_pitfall           → routes to pitfalls.md, auto-apply for new bullets, stage for section replace
record_decision       → routes to decisions.md, requires staging + provenance
refresh_module        → routes to modules/<m>.md, requires staging
update_conventions    → routes to conventions.md, requires staging
archive_stale         → executes archive_section, requires staging
```

#### Supported Operations

```text
create_file
append_section
replace_section
append_to_section
replace_section_content     (new in v0.4: edits body without touching heading or ID)
archive_section
remove_section              (new in v0.4: archives then removes from source file)
rename_heading              (new in v0.4: changes heading text, preserves ID)
```

Each operation is detailed in §15.3–15.10.

#### Server-Side Pipeline

```text
 1. Parse input.
 2. Validate intent against schema.
 3. Validate operation schemas (per-op).
 4. Normalize and validate paths (must be inside .agent-memory; no ".."; no derived paths).
 5. Acquire write lock (§11).
 6. Snapshot target files (record checksums).
 7. For each operation: resolve section identity (§12), compute byte ranges.
 8. Apply all operations to in-memory copies via byte-splice (§21).
 9. Validate resulting Markdown: parses cleanly, schema-conforms for the file category.
10. Run secret scan (§23) with allowlist.
11. Run poisoning checks (§23.4).
12. Check size budgets (per-file, per-section).
13. Check semantic duplication (Jaccard against same-category content).
14. Compute preview diff (for staging or status).
15. Decide apply-vs-stage per category policy.
16. If apply: atomic rename writes. Update FTS5 incrementally (only affected sections).
17. If stage: write staging directory with proposal.json, preview.diff, target-checksums.json, files/.
18. Release lock.
19. Optionally: trigger `git add` for applied durable files (if manifest.git.auto_stage_changes = true). Never commit automatically.
20. Return status.
```

#### Output: Applied

```json
{
  "status": "applied",
  "applied_at": "2026-05-26T12:15:03Z",
  "changed_files": [".agent-memory/modules/auth.md"],
  "affected_sections": [{"file": "modules/auth.md", "section_id": "token-rotation"}],
  "index_updated": true,
  "warnings": []
}
```

#### Output: Staged

```json
{
  "status": "staged",
  "staging_id": "2026-05-26T121500-auth-token-rotation",
  "staging_ttl_seconds": 604800,
  "human_approval_required": true,
  "review_command": "agent-memory review 2026-05-26T121500-auth-token-rotation",
  "message": "Memory update staged. Approval required by manifest policy for 'modules/' category."
}
```

#### Output: Rejected

```json
{
  "status": "rejected",
  "reason": "secret_detected",
  "findings": [
    {
      "type": "github_token",
      "operation_index": 0,
      "approximate_location": "content line 8",
      "allowlist_hint": "If this is a documentation example, wrap in <!-- @secret-scan: allow --> ... <!-- @secret-scan: end --> markers."
    }
  ],
  "required_action": "Rewrite the memory update without copying credentials, or use the allowlist mechanism for legitimate examples."
}
```

### 15.3 Operation: `create_file`

```json
{
  "operation": "create_file",
  "path": "modules/search.md",
  "content": "# Search Module\n<!-- @id: search-module -->\n\n...",
  "if_exists": "reject"
}
```

Rules:

- Path must be inside `.agent-memory/` and match an allowed category from the schema.
- Path must not be a derived/local path written by the server.
- Server inserts `@id` anchors to any headings missing them before write.
- `if_exists`: `reject` (default), `append`, `replace`. `replace` always requires staging regardless of category policy.

### 15.4 Operation: `append_section`

```json
{
  "operation": "append_section",
  "path": "modules/auth.md",
  "parent_section_id": "auth-module",
  "heading": "Token Rotation",
  "heading_level": 2,
  "content": "## Token Rotation\n\nRefresh tokens are rotated on every successful use.\n",
  "if_exists": "reject"
}
```

Rules:

- If `parent_section_id` present, append inside that section (before next sibling/ancestor heading).
- Else append to end of file.
- The first line of `content` must be a heading matching `(heading, heading_level)`.
- Server inserts the `@id` anchor after the heading on write.
- `if_exists` allowed: `reject` (default), `append` (creates `-2` ID variant), `replace`.

### 15.5 Operation: `replace_section`

```json
{
  "operation": "replace_section",
  "path": "modules/auth.md",
  "section_id": "token-rotation",
  "content": "## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens are rotated...\n",
  "if_missing": "reject"
}
```

Rules:

- Resolves by `section_id` (preferred) or `(heading, heading_level, occurrence)`.
- Replaces content from heading line to (exclusive) next heading at same/higher level.
- `content` must start with the same heading and (for ID-keyed ops) preserve or omit the `@id` anchor — the server enforces the anchor's preservation.
- `if_missing`: `reject` (default), `append` (creates section), `create_file`.

### 15.6 Operation: `append_to_section`

```json
{
  "operation": "append_to_section",
  "path": "pitfalls.md",
  "section_id": "pitfalls-authentication",
  "content": "- Old tests assumed refresh tokens remained stable.\n  Files: internal/auth/refresh_test.go\n  Confidence: confirmed\n  Recorded: 2026-05-26\n"
}
```

Rules:

- Inserts at end of section (before next heading at same/higher level).
- Subject to duplicate detection (skips bullet-level duplicates).
- Auto-apply for categories tagged `append_auto_apply: true` in schema (e.g., `pitfalls.md`).

### 15.7 Operation: `replace_section_content`

New in v0.4. Replaces section *body* without touching heading or ID.

```json
{
  "operation": "replace_section_content",
  "path": "modules/auth.md",
  "section_id": "token-rotation",
  "content": "Refresh tokens are rotated on every successful use.\n\nSources:\n- file: internal/auth/refresh.go\n"
}
```

Rules:

- `content` must NOT start with a heading.
- Server keeps the existing heading line + `@id` anchor, replaces everything until next heading.
- Useful when only the body changed and the heading/ID must remain stable.

### 15.8 Operation: `archive_section`

```json
{
  "operation": "archive_section",
  "path": "modules/auth.md",
  "section_id": "cookie-refresh-legacy",
  "archive_path": "archive/2026-05-auth-cookie-v1.md",
  "replacement": "## Cookie Refresh Legacy Flow\n<!-- @id: cookie-refresh-legacy -->\n\nArchived: see `archive/2026-05-auth-cookie-v1.md`.\n"
}
```

Rules:

- Server copies the section content to `archive_path`.
- Server replaces the section body with `replacement`.
- `archive_path` must be inside `archive/` and not already exist.
- Archive files are write-once; future `archive_section` operations cannot overwrite.
- Archive operation never destroys content.
- Always staged (archive is durable).

### 15.9 Operation: `remove_section`

New in v0.4. Archive-first removal.

```json
{
  "operation": "remove_section",
  "path": "modules/auth.md",
  "section_id": "cookie-refresh-legacy",
  "archive_path": "archive/2026-05-auth-cookie-v1.md",
  "reason": "Replaced by token-rotation as of refresh.go refactor"
}
```

Rules:

- Server archives the section to `archive_path` (same rules as `archive_section`).
- Then splices the section out of the source file entirely (heading included).
- Requires staging.
- Use case: section is no longer relevant at all, not even as a pointer.

### 15.10 Operation: `rename_heading`

New in v0.4. ID-preserving heading rename.

```json
{
  "operation": "rename_heading",
  "path": "modules/auth.md",
  "section_id": "token-rotation",
  "new_heading": "Refresh Token Rotation",
  "new_heading_level": 2
}
```

Rules:

- ID is preserved.
- Heading text changes; level may change (constrained to ±1 to avoid restructuring).
- Bytes outside the heading line are untouched.

### 15.11 `memory.status`

Returns memory health and metadata.

#### Output

```json
{
  "memory_version": "0.4.0",
  "repo": "my-service",
  "active_branch": "feature/auth-rotation",
  "durable_files": 18,
  "archive_files": 7,
  "local_sessions": 4,
  "local_current_files": 3,
  "orphan_local_files": ["local/current.old-branch.md"],
  "index_size_bytes": 6120,
  "current_size_bytes": 2400,
  "staged_updates": [
    {
      "id": "2026-05-26T121500-auth-token-rotation",
      "intent": "refresh_module",
      "age_seconds": 14400,
      "ttl_remaining_seconds": 590400,
      "target_files": ["modules/auth.md"],
      "drift_detected": false
    }
  ],
  "stale_notes": ["modules/payments.md"],
  "security": {
    "last_secret_scan": "passed",
    "allowlisted_regions": 2,
    "untrusted_sources": 0
  },
  "git": {
    "track_local": false,
    "track_sessions": false,
    "ignored_local_state": true,
    "merge_driver_installed": true
  },
  "lock": {
    "held": false,
    "stale_recoveries_last_24h": 0
  }
}
```

---

## 16. Human Approval and Staging

### 16.1 Per-Category Policy

Approval policy is declared per category in `manifest.yaml`:

```yaml
updates:
  approval:
    decisions: stage
    conventions: stage
    modules: stage
    pitfalls_replace: stage
    pitfalls_append: apply
    archive: stage
    current: apply
    current_shared: apply
    sessions: apply
    index: server_only
```

`stage` = staged for human review. `apply` = applied immediately after validation. `server_only` = agents cannot write.

### 16.2 Recommended Defaults

```yaml
updates:
  approval:
    decisions: stage
    conventions: stage
    modules: stage
    pitfalls_replace: stage
    pitfalls_append: apply
    archive: stage
    current: apply
    current_shared: apply
    sessions: apply
    index: server_only
```

Rationale:

- Durable + team-visible → staged.
- Append-only journals where each entry is small (pitfalls, sessions) → applied.
- Local ephemeral state → applied.
- Index is server-maintained → no agent writes.

### 16.3 Staging Directory Layout

```text
.agent-memory/staging/
  2026-05-26T121500-auth-token-rotation/
    proposal.json
    preview.diff
    target-checksums.json      # per-operation-target hashes (not per-file)
    files/
      modules/auth.md          # the proposed file content
```

`target-checksums.json` records one check entry per operation, scoped to the operation's target — not a whole-file hash. This is the v0.4.1 fix: unrelated edits elsewhere in the same file do not trigger drift.

```json
{
  "captured_at": "2026-05-26T12:15:00Z",
  "operation_targets": [
    {
      "operation_index": 0,
      "operation": "replace_section",
      "path": ".agent-memory/modules/auth.md",
      "section_id": "token-rotation",
      "policy": "require_section_content_match",
      "expected_section_hash": "sha256:7d3f..."
    },
    {
      "operation_index": 1,
      "operation": "append_to_section",
      "path": ".agent-memory/pitfalls.md",
      "section_id": "pitfalls-authentication",
      "policy": "require_section_resolvable"
    }
  ]
}
```

### 16.4 TTL and Drift

- Staged updates have a TTL (manifest: `staging.ttl_seconds`, default 7 days).
- On `apply`, the server re-resolves each operation's target by `@id` against the current file state, then evaluates a per-operation policy:

| Policy | Applies to | Check |
|---|---|---|
| `require_section_content_match` | `replace_section`, `replace_section_content`, `archive_section`, `remove_section`, `rename_heading` | Resolve `section_id` in the current file; hash the current section content; compare against `expected_section_hash`. |
| `require_section_resolvable` | `append_to_section` | Resolve `section_id` in the current file. Content may have grown since staging — only resolvability matters. |
| `require_file_absent` | `create_file` with `if_exists: reject` | File still does not exist. |
| `require_file_present` | `create_file` with `if_exists: append`/`replace`, `append_section` | File exists; if `append_section` targets a `parent_section_id`, that parent must also resolve. |

- If any check fails, apply aborts with `target_drift`, naming the failing operation and the specific target that drifted. Other operations are not consulted; the proposal is rejected atomically.
- Recovery: `agent-memory rebase <id_prefix>` re-resolves all section IDs against current file state and refreshes the `expected_section_hash` values. The rebased staging is then ready for apply. Rebase fails only if a target section was deleted entirely (not just edited).
- Expired staged updates are listed in `status` but not auto-deleted. `agent-memory clean-staging` removes expired entries.

Byte-offset shifts in the source file from edits to *other* sections do not cause drift, because section identity is `@id`-based, not byte-range-based. This is what makes per-section checksumming work.

### 16.5 Batching

`agent-memory review` (no args) shows a list of all staged updates with summaries. `agent-memory review --since=24h` filters. `agent-memory review --apply-all-trivial` accepts all updates flagged as "trivial" (small content change, same section, no schema warnings) — opt-in convenience for high-trust environments.

### 16.6 CLI Commands

All staging commands accept either a full ID or any unique prefix (Git-style). The prefix is matched against the full `<timestamp>-<slug>` ID; both `2026-05-26T1` and `auth-token` are valid if uniquely matching. On ambiguity the CLI lists candidates and exits non-zero. `--latest` resolves to the most recently staged update.

```bash
agent-memory review                                  # list all staged updates
agent-memory review <id_or_prefix>                   # detail; e.g. `review 2026`, `review auth`, `review auth-token`
agent-memory review --latest                         # detail the most recent
agent-memory apply <id_or_prefix> [--commit]         # apply with per-section drift check
agent-memory apply --latest [--commit]
agent-memory reject <id_or_prefix>
agent-memory reject --latest
agent-memory rebase <id_or_prefix>                   # re-resolve section IDs, refresh expected_section_hash
agent-memory rebase --latest
agent-memory clean-staging                           # remove expired
```

Example of ambiguous prefix:

```text
$ agent-memory apply auth
error: prefix "auth" matches 2 staged updates:
  2026-05-26T121500-auth-token-rotation
  2026-05-26T143012-auth-session-cleanup
hint: use a longer prefix, e.g. `apply auth-token` or `apply auth-session`
```

Interactive selection (TUI via `charmbracelet/bubbletea`) is deferred to v0.5 (see §35.1). For MVP, prefix matching plus `--latest` is sufficient.

### 16.7 Review Output

`agent-memory review <id>` shows:

- Intent and rationale.
- Affected files and sections (by ID, with current heading text).
- Source references and confidence.
- Operation list (semantic, not just diff).
- Generated preview diff (byte-level, after byte-preserving splice).
- Schema validation result.
- Security scan result.
- Drift status.
- Age and TTL remaining.

---

## 17. CLI Commands

### 17.1 MVP

Staging-related commands accept `<id_or_prefix>` (any unique prefix of a staging ID) or `--latest`. See §16.6 for prefix rules.

```bash
agent-memory init [--with-merge-driver]
agent-memory fetch "auth migration" [--budget 6000] [--scope auth]
agent-memory status
agent-memory review [<id_or_prefix> | --latest] [--since 24h]
agent-memory apply (<id_or_prefix> | --latest) [--commit]
agent-memory reject (<id_or_prefix> | --latest)
agent-memory rebase (<id_or_prefix> | --latest)
agent-memory rebuild-index
agent-memory doctor
agent-memory mcp --stdio
agent-memory install claude
agent-memory install codex
agent-memory install cursor
agent-memory install generic
```

### 17.2 Later

```bash
agent-memory compact --stage <path>
agent-memory archive --module auth
agent-memory scan-secrets [--allowlist-check]
agent-memory clean-local
agent-memory clean-staging
agent-memory export
agent-memory import
```

### 17.3 Removed from v0.3

```bash
agent-memory record-session       # → folded into propose_update with intent=session_log
agent-memory propose-update --dry-run
agent-memory apply-update < patch.diff
```

---

## 18. Adapter Strategy

Adapters are the highest-risk component. v0.3 deferred them to "generate per-agent files." v0.4 makes adapter behavior a first-class deliverable.

### 18.1 Adapter Acceptance Criteria

An adapter is acceptable if, on a benchmark task suite, the integrated agent:

- Calls `memory.fetch_context` before reading code in a module it hasn't touched in this session: ≥80% of trials.
- Calls `memory.propose_update` after meaningful durable changes (e.g., new module, changed convention): ≥60% of trials.
- Does NOT generate unified diffs to memory files: 100%.
- Does NOT directly Read/Write files under `.agent-memory/` (bypassing tools): ≥95%.

These thresholds are measured via the eval methodology in §29.

### 18.2 Claude Code Adapter

Generated files:

```text
CLAUDE.md                                       (instructions appended/updated)
.claude/skills/project-memory/SKILL.md
.claude/settings.json                           (only adds MCP server config)
```

### 18.3 Codex Adapter

Generated files:

```text
AGENTS.md
.agents/skills/project-memory/SKILL.md
```

### 18.4 Cursor Adapter

Generated files:

```text
.cursor/rules/agent-memory.mdc
```

### 18.5 Worked Example: Claude Code SKILL.md

Adapters generate something like this. Iterating on this prompt is part of adapter quality.

```md
---
name: project-memory
description: Use the project-memory MCP server (agent-memory) before reading project files, and after making meaningful changes that durable knowledge should record. Triggers any time you'd otherwise spend many tool calls re-learning project conventions, module boundaries, or prior decisions.
---

# Project Memory

This repository has a structured memory layer accessed via three MCP tools:

- `memory.fetch_context` — get a compact pack of project context.
- `memory.propose_update` — record durable knowledge after meaningful change.
- `memory.status` — check memory health and stale areas.

## When to fetch

**Always** call `memory.fetch_context` (with no arguments — empty query) at the start of any task that touches code you haven't worked on in this session. The bootstrap pack costs ~5K characters and saves dozens of file reads.

**Call with a query** before working on a specific area:

```json
{
  "tool": "memory.fetch_context",
  "input": {"query": "auth refresh flow", "scope": ["auth"]}
}
```

Prefer one broad fetch over multiple narrow ones — the server budgets and ranks for you.

## When to propose updates

After a meaningful change, propose a memory update. Examples of meaningful:

- New module created, removed, or significantly restructured.
- Architectural decision made and committed.
- Pitfall discovered (a thing that wasted >15 minutes; record it).
- Convention changed (test command, lint config, branching).
- Task state changed (use `intent: update_current` for branch-local progress).

Examples of NOT meaningful:

- Trivial bug fix in obvious code.
- Refactor with no behaviour change.
- Style-only edits.

## How to propose

The server handles all the fragile parts: section identity, Markdown editing, secret scanning, staging. You provide:

- `intent` — what kind of memory this is.
- `rationale` — why this update.
- `operations` — section-level changes (NEVER unified diffs).
- `sources` — files, tests, user statements that back the update.

Section operations reference sections by their `section_id` (the `<!-- @id: ... -->` anchor). Look at the fetched context to find IDs.

Example: replacing a section.

```json
{
  "tool": "memory.propose_update",
  "input": {
    "intent": "refresh_module",
    "rationale": "Auth refresh flow now uses token rotation.",
    "operations": [{
      "operation": "replace_section",
      "path": "modules/auth.md",
      "section_id": "token-rotation",
      "content": "## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate on every successful use.\n\nSources:\n- file: internal/auth/refresh.go\n"
    }],
    "sources": [{"type": "file", "ref": "internal/auth/refresh.go"}]
  }
}
```

## What never to do

- Do NOT generate unified diffs.
- Do NOT call dry-run or apply — the server decides based on project policy.
- Do NOT directly read or write files under `.agent-memory/` (use the tools).
- Do NOT put secrets, credentials, customer data, or raw `.env` values in any memory update.
- Do NOT record speculative "might be useful later" notes — record confirmed knowledge.
- Do NOT try to delete content. Use `archive_section` (preserves history) or `remove_section` (still archives).

## Approval flow

For some categories (decisions, conventions, modules, archive), updates are staged for human review. The server will return `status: staged` — that's expected, not an error. The human reviews via CLI and applies.

For local-state categories (current task, sessions, new pitfall bullets), updates apply immediately.

## On branch switches

The server resolves the current branch automatically. After `git checkout`, your next `memory.fetch_context` returns the new branch's local state.
```

### 18.6 Generic Adapter

`AGENTS.md` with shorter version of the above, for any MCP-capable agent. Includes CLI fallbacks for hosts without MCP.

### 18.7 Init Behavior with Existing Files

If `CLAUDE.md` / `AGENTS.md` / `.cursor/rules/` already exist:

- The adapter generator never overwrites.
- It writes its block between idempotent markers:

  ```md
  <!-- agent-memory:begin -->
  ...
  <!-- agent-memory:end -->
  ```

- On re-install, only the marked block is updated.
- Outside the markers, the user's content is untouched.

---

## 19. Bootstrap / Auto-Inject Strategy

### 19.1 Bootstrap Pack

```text
local/current.<active-branch>.md
local/current.shared.md
conventions.md
index.md summary (server-maintained)
```

### 19.2 Constraints

- Bootstrap budget is separate from fetch_context budget (manifest: `budgets.bootstrap_chars`, default 12 000).
- Bootstrap never includes archived content.
- Bootstrap never includes every module file (use `index.md` for navigation).
- Bootstrap includes explicit instruction to call `fetch_context` for module-specific work.
- Bootstrap clarifies that local state is per-branch and gitignored.

### 19.3 Injection Mechanism

Bootstrap is injected via adapter, not via a generic MCP capability. For Claude Code, the SKILL.md tells Claude to call `memory.fetch_context` at session start. For hosts without skill-style triggers, the AGENTS.md tells the agent the same.

---

## 20. Search and Ranking

### 20.1 Shadow Index

Markdown is the source of truth. SQLite at `.agent-memory/meta/index.sqlite` is a local rebuildable shadow index. Gitignored.

### 20.2 FTS5 Schema

```sql
CREATE VIRTUAL TABLE memory_search USING fts5(
  file,
  section_id,
  title,
  headings,
  content,
  tags,
  tokenize='porter unicode61'
);

CREATE TABLE memory_sections (
  file TEXT NOT NULL,
  section_id TEXT NOT NULL,
  heading TEXT NOT NULL,
  heading_level INTEGER NOT NULL,
  byte_start INTEGER NOT NULL,
  byte_end INTEGER NOT NULL,
  content_hash TEXT NOT NULL,
  PRIMARY KEY (file, section_id)
);

CREATE TABLE memory_docs (
  file TEXT PRIMARY KEY,
  category TEXT NOT NULL,
  freshness TEXT,
  confidence TEXT,
  last_modified TEXT,
  committed INTEGER DEFAULT 1,
  local_state INTEGER DEFAULT 0,
  archived INTEGER DEFAULT 0,
  size_bytes INTEGER,
  checksum TEXT
);
```

### 20.3 Incremental Updates

After every successful write:

1. Compute affected `(file, section_id)` set from the operation.
2. For each affected section: `DELETE FROM memory_search WHERE file=? AND section_id=?` then `INSERT` with new content.
3. Update `memory_sections` byte ranges (re-scan only the affected file, since byte offsets shift downstream of the splice).
4. Update `memory_docs.checksum` and `size_bytes`.

This is bounded work per write: O(affected sections + file scan), not O(total memory).

`rebuild-index` does a full rebuild from scratch — used on demand, on schema migrations, or via `doctor`.

### 20.4 Ranking Signals

Positive (multiplicative):

- Query term match (BM25 score is base).
- Scope match (×2.0).
- Fresh files (×1.5).
- Active branch references (×1.3).
- Decisions/pitfalls referencing changed files (×1.4).

Negative:

- Archived (×0.4).
- Stale-marked (×0.6).
- Very large files (linear penalty above per-file budget threshold).
- Low-confidence inferred notes (×0.8).
- Duplicate content (collapsed before ranking).

### 20.5 Context Pack Assembly

```text
1. Resolve active branch, compute bootstrap set.
2. Parse query and scope.
3. Search SQLite FTS5 for the query.
4. Apply ranking multipliers.
5. Chunk long files by section (sections are the unit of inclusion).
6. Deduplicate semantic overlap (Jaccard > 0.85 → keep higher-scoring).
7. Apply budget greedily, descending score.
8. Render one Markdown context pack, preserving section headers and @id anchors.
9. Return provenance and stale warnings.
```

### 20.6 Why Not Just Grep

Honest answer: for small memory (<30 files), BM25 over FTS5 doesn't beat ranked grep by much. The reason to use FTS5 anyway:

- Section-level chunking is needed regardless.
- Scope/freshness/archive metadata needs structured storage.
- Incremental updates are cheap once we have the table.
- Future ranking improvements (e.g., embeddings) have a place to live.

For repos where memory stays tiny, the index is mostly inert overhead. That's acceptable — the cost is small (<200 ms init, kilobytes on disk).

---

## 21. Byte-Preserving Markdown Update Engine

This is the most important technical detail. It is what makes the product not produce noisy diffs.

### 21.1 The Problem with AST Round-Trips

Common Markdown libraries (including `goldmark`) parse to AST and render back to text. The render is *not* byte-identical to the input:

- List markers may normalize (`*` → `-`).
- Code fence styles may normalize.
- Trailing whitespace, blank-line counts may change.
- Indentation may regularize.

If every `propose_update` round-trips a file through render, every change produces a noisy diff well beyond the actual edit. This breaks `git blame`, causes spurious review noise, and creates merge conflicts where none should exist.

### 21.2 The v0.4 Approach

Never render. Splice.

```text
1. Parse the AST to locate the byte offsets of the target section heading and the end of the section (start of next heading at same/higher level, or EOF).
2. The section's byte range is [start, end).
3. The new section content (provided by the agent) is validated: must parse as Markdown, must start with the correct heading, must contain the @id anchor.
4. Splice: new_bytes = source[:start] + new_content + source[end:]
5. Write atomically (temp file + rename).
```

The rest of the file is byte-identical to before. `git blame` is preserved for unchanged regions.

### 21.3 ID Anchor Maintenance

The one exception: on first scan of a file with un-anchored headings, the engine writes `<!-- @id: ... -->` lines after each heading. This is a one-time normalization. After that, the engine never modifies anchors except as part of explicit operations (`create_file` with `content` that includes anchors, `rename_heading` which doesn't touch the anchor at all).

### 21.4 Validation Rules

Before splice:

- Provided `content` must parse as Markdown (via goldmark AST traversal — but the rendered output is discarded; we only check parseability).
- For `replace_section`: first non-blank line of `content` must be a heading matching `(heading, heading_level)` of the target. If target has an `@id` anchor and `content` includes one, they must match. If `content` omits the anchor, server inserts the existing anchor.
- For `append_to_section`: `content` must not contain a heading at level ≤ target's level.
- For `create_file`: top-level heading required; ID is generated.
- For `archive_section` and `remove_section`: archive copy is byte-identical to the removed section in the source file.

### 21.5 Edge Cases

- **Trailing newlines.** The engine preserves the exact newline count between sections. If a section ended with `\n\n` before, it still does after splice (the agent's `content` is trimmed and re-padded to match).
- **Fenced code blocks containing headings.** AST tells us the heading is inside a code fence; we ignore it as a section marker. Section boundary detection respects code fences.
- **HTML comments outside `@id` anchors.** Preserved. Not interpreted.
- **YAML front matter.** Preserved. Not currently parsed for metadata (future work).
- **CRLF vs LF line endings.** On write, the engine preserves the file's existing line-ending convention.

### 21.6 Failure Modes

| Case | Behavior |
|---|---|
| Section ID not found | `if_missing` policy decides: reject / append / create_file |
| Multiple sections with same heading and no ID provided | Require `occurrence` field; else reject |
| Content fails to parse | Reject with parse error location |
| Content contains a different heading than declared | Reject |
| Source file changed between snapshot and write | Reject; report drift |
| Disk write fails after partial atomic rename | Rollback via journal; lock-holder responsibility |

### 21.7 Why Not Existing Libraries

No off-the-shelf Go Markdown library does byte-preserving splice. `goldmark` is good for parsing but not for byte-faithful output. The engine is a thin wrapper around goldmark's AST that records byte offsets and does string manipulation directly on the source bytes. This is hundreds of lines of code, not thousands.

---

## 22. Compaction and Archival

### 22.1 Archival Compaction

Compaction is never destructive. Main file after compaction retains:

- Current interface / facts.
- Active constraints.
- Current file references.
- Key tests.
- Link to archived historical detail.
- Freshness marker.

Historical detail moves to `archive/YYYY-MM-topic.md`. Archive files are write-once.

### 22.2 Agent-Facing

Use `archive_section` (preserves pointer) or `remove_section` (archives + removes). Never a raw delete.

### 22.3 Stale Detection

A file is "stale" if:

- Its `last_modified` is older than `archive.stale_threshold_days` (manifest, default 60), AND
- It references files in the repo that have changed since `last_modified`.

`status` lists stale files. `doctor` suggests sections to compact.

### 22.4 Auto-Compaction

Not in MVP. Manual flow only. v0.5 will introduce assisted compaction (server proposes, agent generates new content, human approves via standard staging).

---

## 23. Security Model

### 23.1 Threats

| Threat | Example | Mitigation |
|---|---|---|
| Secret leakage | Agent writes API token into memory | Regex + entropy scan, allowlist mechanism, reject on hit |
| Prompt injection | External docs instruct agent to alter memory | Provenance, untrusted-source labels, staging |
| Memory poisoning | Incorrect durable facts recorded | Required rationale, sources, staging |
| Bloat attack | Agent appends huge irrelevant context | Size budgets, duplicate detection |
| Stale fact persistence | Old behavior remains in main memory | Freshness markers, status warnings |
| Unsafe archive fetch | Archived stale details influence current work | Archive penalty in ranking |
| Invalid Markdown update | Agent generates malformed replacement | AST validation, schema check |
| Local state conflicts | `current.md` committed by multiple developers | Per-branch local state, `.gitignore` defaults |
| Race conditions | Two agents writing concurrently | Single-writer lock, drift detection |
| ID collision | Two sections same intended ID | Auto-suffix `-2`, `-3` |

### 23.2 Secret Scanner: Regex Set

```text
AWS access keys (AKIA, ASIA prefixes)
AWS secret keys (40-char base64-like with entropy)
GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_)
GitLab tokens (glpat-)
OpenAI API keys (sk-...)
Anthropic API keys (sk-ant-...)
Stripe (sk_live_, pk_live_, rk_live_)
JWTs (three base64 segments separated by dots, header validates)
SSH private keys (-----BEGIN ... PRIVATE KEY-----)
PEM private keys
.env-style assignments with high-entropy RHS
Generic Shannon-entropy detection (threshold 4.5, length >= 32)
```

### 23.3 Behavior on Detection

```text
1. Identify token type and location (operation index, approximate line).
2. Reject the entire propose_update (no partial application).
3. Do not echo the full token back to the agent (security: avoid round-trip leakage).
4. Return required_action with allowlist instructions.
```

### 23.4 Poisoning Guard

Rules:

- Content from external docs, issues, web pages, or generated output is untrusted by default.
- Untrusted content cannot become durable memory without explicit source labeling (`source.type: external`).
- Instructions inside repository documentation are not automatically trusted as memory policy.
- Memory policy lives in `manifest.yaml` and adapter-generated files. Markdown files in `.agent-memory/` are not policy.
- Staged updates preserve source/rationale metadata for human review.

### 23.5 Provenance Levels

Source types:

```text
file        - repository source file
test        - test file
user        - explicit user statement in conversation
session     - earlier session note
inference   - agent reasoning, no direct source
external    - external doc, issue, web page
```

Confidence levels:

```text
confirmed       - backed by tests or direct user
inferred        - agent reasoning from code
user-provided   - user said so, no code verification
stale           - was confirmed, but underlying code has changed
unknown
```

For staged categories, schema enforces: `sources` non-empty, `confidence` ∈ {`confirmed`, `inferred`, `user-provided`}.

### 23.6 Allowlist for Documentation Examples

Legitimate use case: explaining a token format in `pitfalls.md` or `modules/auth.md`. Example:

```md
## Token Formats
<!-- @id: token-formats -->

<!-- @secret-scan: allow reason="documentation example" -->
GitHub tokens start with `ghp_` followed by 36 alphanumerics:
- `ghp_AaBbCcDdEeFfGgHhIiJjKkLlMmNnOoPpQqRr` (example, not a real token)
<!-- @secret-scan: end -->
```

Rules:

- Allowlisted regions skip secret detection.
- Region must have an explicit `reason`.
- Allowlisted region count is reported in `status` for audit.
- Nested allowlists are not supported (flat regions only).
- During staged review, the reviewer sees a warning "this proposal contains allowlisted secret-scan regions" — human confirmation is required for new allowlisted regions to land.

### 23.7 No Silent Bypass

There is no global `disable_secret_scan` flag. The only way to bypass is per-region allowlist with a reason. This is a deliberate design choice.

---

## 24. Git Integration

### 24.1 Default Behavior

- Durable memory files are modified locally and left for the developer to commit (or auto-committed via `--commit` on `apply`).
- Local state is gitignored.
- Derived files are gitignored.
- Staging is gitignored.

### 24.2 Safe Write Pipeline

```text
1. Read current memory files.
2. Acquire lock (§11).
3. Snapshot checksums.
4. Apply structured operations in memory via byte-splice.
5. Run secret scan with allowlist.
6. Validate schema for each affected file.
7. Write to temp files.
8. Atomic rename.
9. Update FTS5 incrementally.
10. Release lock.
11. Optionally git add (never commit) durable memory files.
```

### 24.3 Dirty Working Tree

If the repo has uncommitted changes:

- `fetch_context`, `status`, `review` work normally.
- `propose_update` and `apply` work but warn the user/agent.
- `--commit` is suppressed unless explicit override.

### 24.4 `.agent-memory/.gitignore`

`init` writes:

```gitignore
# Local state
local/
sessions/
staging/

# Derived
meta/index.sqlite
meta/index.sqlite-*
meta/*.db
meta/*.db-*
meta/lock.json
meta/cache/
```

### 24.5 Merge Strategy for Durable Memory

This is new in v0.4 and matters for team usage.

**Recommendation 1: Section-per-topic discipline.** Each topic gets its own section with a stable `@id`. Two developers editing different sections of the same file produce non-overlapping diffs, which Git merges cleanly.

**Recommendation 2: Custom merge driver (opt-in).** `agent-memory init --with-merge-driver` installs a `.gitattributes` entry:

```text
.agent-memory/**/*.md merge=agent-memory
```

And a `merge.agent-memory.driver` in `.git/config` invoking:

```bash
agent-memory merge-driver %O %A %B %P
```

The driver:

1. Parses base, ours, theirs as Markdown with section IDs.
2. For each section ID:
   - If only one side changed: take that change.
   - If both changed: keep both versions in a `<!-- @merge-conflict -->` block for human resolution.
   - If section added only on one side: include it.
   - If section removed on one side: keep the surviving version, log warning.
3. Writes result to `%A`.

This is opt-in because not all teams want custom merge drivers. The default is Git's built-in merge, with the section-per-topic discipline as the guard.

**Recommendation 3: Pre-commit hook (opt-in).** Optionally, `init` can install a hook that runs `agent-memory doctor` before commit and fails on validation errors (duplicate IDs, schema violations).

---

## 25. Formal Schema

The schema lives in `.agent-memory/meta/schema.yaml`. It is human-editable. The server validates every write against it. v0.3 declared this exists; v0.4 specifies the format.

### 25.1 Schema Example

```yaml
version: "0.4"

categories:

  index:
    file: index.md
    server_managed: true
    agent_writable: false

  conventions:
    file: conventions.md
    section_id_required: true
    section_schema:
      required_top_level_heading: true
      sections:
        commands:
          required: true
          must_contain_subsections: [test, lint, build]
        branching:
          required: true
        review:
          required: false
    approval: stage
    provenance:
      required_for_new_sections: true
      allowed_source_types: [file, test, user]

  decisions:
    file: decisions.md
    section_id_required: true
    section_schema:
      per_section_required_fields:
        - name: Date
          pattern: '^\d{4}-\d{2}-\d{2}$'
        - name: Status
          enum: [active, superseded, deprecated]
        - name: Confidence
          enum: [confirmed, inferred, user-provided]
        - name: Context
        - name: Decision
        - name: Consequences
      per_section_optional_fields:
        - name: Sources
        - name: Supersedes
        - name: Superseded by
    approval: stage
    provenance:
      required: true
      allowed_source_types: [file, test, user]
      forbidden_source_types: [external, inference]

  pitfalls:
    file: pitfalls.md
    section_id_required: true
    section_schema:
      bullet_format:
        required_fields: [Files, Confidence, Recorded]
    approval:
      append_bullet: apply
      replace_section: stage
    provenance:
      required_for_new_sections: true

  modules:
    file_glob: modules/*.md
    section_id_required: true
    approval: stage
    provenance:
      required: true

  archive:
    file_glob: archive/*.md
    write_once: true
    approval: stage
    provenance:
      required: false

  current:
    file_glob: local/current.*.md
    section_id_required: false
    approval: apply
    git_tracked: false
    provenance:
      required: false

  sessions:
    file_glob: sessions/*.md
    section_id_required: false
    approval: apply
    git_tracked: false

budgets:
  max_file_chars: 20000
  max_section_chars: 4000
  bootstrap_chars: 12000
  fetch_context_chars: 24000
  index_max_lines: 200

staging:
  ttl_seconds: 604800
  auto_clean_expired: false

security:
  secret_scan: true
  entropy_threshold: 4.5
  entropy_min_length: 32
  reject_external_untrusted_durable: true
```

### 25.2 Validation Pipeline

On every write:

1. Identify category by path or glob.
2. Check `agent_writable`.
3. Check approval policy → decide stage vs apply.
4. Check section_id_required → enforce anchor presence.
5. Validate per-section schema for affected sections.
6. Validate provenance against requirements.
7. Validate budgets.

Validation failures reject the proposal with a structured error.

### 25.3 Schema Migrations

The schema's `version` field gates the manifest version. Bumping the schema version requires:

- `agent-memory rebuild-index` (re-scans all files against new schema).
- Optional `agent-memory migrate` for non-trivial structural changes (deferred to v0.5+).

---

## 26. Configuration

### 26.1 Manifest

`.agent-memory/meta/manifest.yaml`:

```yaml
version: "0.4.0"

project:
  name: my-service
  root: .

budgets:
  bootstrap_chars: 12000
  fetch_context_chars: 24000
  max_file_chars: 20000

updates:
  approval:
    decisions: stage
    conventions: stage
    modules: stage
    pitfalls_replace: stage
    pitfalls_append: apply
    archive: stage
    current: apply
    current_shared: apply
    sessions: apply
    index: server_only

staging:
  ttl_seconds: 604800

security:
  secret_scan: true
  reject_untrusted_durable_updates: true

git:
  track_local: false
  track_sessions: false
  auto_stage_changes: false
  auto_commit: false
  commit_message_prefix: "chore(memory):"
  merge_driver_installed: false

archive:
  enabled: true
  stale_threshold_days: 60

concurrency:
  lock_ttl_seconds: 30
  wait_timeout_seconds: 10

local_state:
  per_branch: true
  shared_file_enabled: true
  cleanup_orphans_warning_days: 90

sessions:
  per_branch: false

eval:
  benchmark_corpus_url: null   # for adapter quality eval; optional
```

### 26.2 Schema

See §25.

---

## 27. Go Implementation Plan

### 27.1 Layout

```text
cmd/
  agent-memory/
    main.go

internal/
  app/
    app.go

  cli/
    init.go
    fetch.go
    status.go
    review.go
    apply.go
    reject.go
    rebase.go
    rebuild_index.go
    doctor.go
    clean.go
    install.go
    mcp.go
    merge_driver.go

  mcp/
    server.go
    tools.go
    types.go

  memory/
    fetch.go
    update.go
    archive.go
    staging.go
    status.go

  index/
    sqlite.go
    incremental.go
    rebuild.go
    query.go
    ranking.go

  markdown/
    parse.go            # goldmark AST traversal
    section_id.go       # ID anchor management
    splice.go           # byte-preserving splice
    validate.go

  schema/
    schema.go
    validate.go
    yaml.go

  security/
    secrets.go
    rules.go
    entropy.go
    allowlist.go
    poisoning.go

  lock/
    lock.go             # advisory file lock on meta/lock.json

  git/
    branch.go           # current branch resolution
    diff.go
    commit.go
    status.go
    merge_driver.go

  adapters/
    claude.go
    codex.go
    cursor.go
    generic.go

  config/
    manifest.go

  fs/
    atomic.go
    paths.go

pkg/
  protocol/
    types.go
```

### 27.2 Library Choices

| Need | Library |
|---|---|
| CLI | `cobra` |
| MCP | `github.com/modelcontextprotocol/go-sdk` |
| SQLite | `modernc.org/sqlite` (CGo-free, simpler distribution) |
| Markdown AST | `yuin/goldmark` (AST only; bytes from source) |
| YAML | `gopkg.in/yaml.v3` |
| JSON Schema (for IPC validation) | `xeipuuv/gojsonschema` |
| Diff preview | `sergi/go-diff` |
| Git | shell-out to system `git` for MVP |
| File locks | `gofrs/flock` (cross-platform advisory locks) |
| Entropy | small in-house implementation |

### 27.3 Why CGo-Free SQLite

`modernc.org/sqlite` is pure-Go and avoids the cross-compilation pain of `mattn/go-sqlite3`. It's slower than the CGo binding but the workload is tiny (KBs to MBs). The portability win is worth it.

### 27.4 Why Go (retained from v0.3)

Single binary, simple deployment, strong fs/process tooling, good performance, easier iteration than Rust, more structured than Python for a durable CLI, no Node runtime, official MCP SDK exists, local security-sensitive tools benefit from minimal runtime dependencies.

Rust path remains open for the index layer if profiling proves Go insufficient. Not expected in v0.4.

---

## 28. Competitive Positioning

This section is new in v0.4. The product must be situated against alternatives.

### 28.1 Alternatives

| Alternative | What it does | Where it falls short |
|---|---|---|
| `AGENTS.md` / `CLAUDE.md` | Single markdown instruction file. | Static; bloats; no search; no validation; no per-task state. |
| Cursor `.cursor/rules` | Rule files for Cursor only. | Cursor-only; no cross-agent. |
| Claude Code Skills | Reusable skill definitions. | Skills are behaviour, not memory; complementary. |
| `mem0`, `letta`, `zep` | Vector memory for agents. | General-purpose; not repo-scoped; require backends. |
| Memory-bank Markdown projects | Curated `.memory/` folders. | No validation, no staging, no concurrency, no secret scan. |
| Git itself | Commit history. | Not searchable for agents; no semantic structure. |

### 28.2 What `agent-memory` Adds

- **Section-level structured updates with stable IDs** — agents can edit semantically without diffing.
- **Byte-preserving Markdown engine** — no churn, clean git history.
- **Secret scanning and allowlist** — safety before persistence.
- **Per-category approval policy** — durable knowledge is reviewed; ephemeral isn't.
- **Branch-aware local state** — current task follows the branch.
- **Concurrency safety** — multi-agent writes don't corrupt.
- **Compact MCP surface** — three tools, learnable.
- **Adapter-validated agent behaviour** — not just file generation.

### 28.3 When Not to Use This

- Repo with <30 files and one developer: `AGENTS.md` is sufficient.
- Refactor-heavy projects where memory goes stale in days: maintenance overhead exceeds value.
- Strict offline environments forbidding even local SQLite: too far from spec.
- Teams not willing to enforce per-category approval: defeats safety value.

### 28.4 Coexistence

`agent-memory` does not replace `AGENTS.md` or `CLAUDE.md`. The adapters generate these files with idempotent markers, pointing the agent to the memory tools. Project-specific human-authored guidance in `AGENTS.md` outside the markers is preserved.

---

## 29. Evaluation Methodology

This section is new in v0.4. Without evaluation, "agents stop rediscovering" is a hope.

### 29.1 Benchmark Corpus

A curated set of repositories with seeded memory and ground-truth task lists:

- Small repo (~50 files, 1 module): 10 tasks.
- Medium repo (~500 files, 5 modules): 20 tasks.
- Large repo (~5000 files, monorepo with 15+ modules): 20 tasks.

Each task has:

- A canonical implementation (golden output).
- Expected file touches.
- Expected memory tool calls (fetch_context patterns).
- Optional: expected memory updates after task.

Repos are published as fixtures with `.agent-memory/` pre-populated.

### 29.2 Metrics

**Cost metrics (lower is better):**

- Tokens consumed before first edit.
- Number of file reads (Read/Grep/Glob calls).
- Wall-clock time to task completion.

**Quality metrics:**

- Task completion rate against golden output.
- Edit precision (files actually changed vs. files needing change).
- Memory tool call rate at expected trigger points (e.g., before touching new module).

**Memory hygiene metrics:**

- Stale fact detection rate.
- Spurious update rate (proposed updates rejected by human review).
- Duplicate-suppression rate.
- Allowlist false-positive rate (legitimate examples blocked).

### 29.3 Comparison Baselines

For each task, run three configurations:

1. Agent + repo with no memory tools (raw baseline).
2. Agent + repo with `AGENTS.md` instructions but no `agent-memory` server.
3. Agent + repo with `agent-memory` integrated.

Difference between (2) and (3) is the marginal value of the product.

### 29.4 Cadence

- On every release candidate: full benchmark.
- Weekly against latest stable agent versions.
- Public dashboard of agent behaviour over time.

### 29.5 Adapter Acceptance Thresholds (referenced from §18.1)

- Call `memory.fetch_context` before reading code in untouched module: ≥80%.
- Call `memory.propose_update` after meaningful durable change: ≥60%.
- Never generate unified diffs to memory: 100%.
- Never bypass tools by direct file edits: ≥95%.

These are adapter-level thresholds, not product-level — adapter quality is the bottleneck.

### 29.6 Open Eval Question

It is currently unclear which agent host will show the largest delta. Hypothesis: hosts with strong skill/rule mechanisms (Claude Code, Cursor) outperform hosts with weaker steering (generic MCP clients). Adapter investment should prioritize accordingly.

---

## 30. MVP Scope

### 30.1 v0.4 Must Have

CLI:

```text
init [--with-merge-driver]
fetch
status
review (list, detail, --since)
apply (with --commit)
reject
rebase
rebuild-index
doctor
mcp --stdio
install (claude, codex, cursor, generic)
```

MCP tools:

```text
memory.fetch_context
memory.propose_update
memory.status
```

Memory layout:

```text
.agent-memory/index.md (server-managed)
.agent-memory/conventions.md
.agent-memory/decisions.md
.agent-memory/pitfalls.md
.agent-memory/modules/
.agent-memory/archive/
.agent-memory/local/current.<branch>.md
.agent-memory/local/current.shared.md
.agent-memory/sessions/
.agent-memory/staging/
.agent-memory/meta/manifest.yaml
.agent-memory/meta/schema.yaml
.agent-memory/meta/index.sqlite
.agent-memory/meta/lock.json
.agent-memory/.gitignore
```

Engine:

```text
Byte-preserving Markdown splice
Section identity with @id anchors
Concurrency lock with stale recovery
Incremental SQLite FTS5 updates
Per-category schema validation
Secret scanner with allowlist
Staging with TTL and drift detection
Git merge driver (opt-in)
Branch-aware local state
```

Adapters with worked SKILL.md:

```text
Claude Code
Codex
Cursor
Generic AGENTS.md
```

Eval:

```text
Benchmark corpus (initial 3 repos)
Adapter acceptance thresholds
```

### 30.2 v0.4 Should Not Have

- Vector search.
- Remote sync.
- Web UI.
- Multi-user permissions.
- Cloud backend.
- Complex knowledge graph.
- Automatic destructive compaction.
- IDE extension.
- Agent-generated unified diffs.
- Agent-controlled dry-run/apply.
- Distributed coordination.
- Multi-repo workspace memory.

---

## 31. MVP Milestones

### M1 — Foundations

```text
agent-memory init (manifest.yaml, schema.yaml, .gitignore, layout)
agent-memory status
Concurrency lock implementation
Byte-preserving Markdown engine skeleton
Section ID assignment pass
agent-memory doctor (basic)
```

Acceptance: init creates valid layout. Doctor passes on freshly init'd repo. Two concurrent agents do not corrupt files.

### M2 — Fetch and Index

```text
SQLite FTS5 shadow index (incremental)
agent-memory fetch
memory.fetch_context MCP tool
Branch-aware local state resolution
Bootstrap pack assembly
```

Acceptance: empty query returns bootstrap. Query returns ranked relevant sections. Switching branches changes bootstrap output. Index updates incrementally on simulated edits.

### M3 — Structured Updates

```text
memory.propose_update MCP tool
Operations: create_file, append_section, replace_section, append_to_section, replace_section_content
Schema validation
Secret scanner with allowlist
Per-category approval routing
```

Acceptance: agent can edit a section via ID without diffing. Schema violations rejected. Secrets rejected. Approval routing matches per-category policy.

### M4 — Archival and Removal

```text
archive_section
remove_section
rename_heading
Archive ranking penalty
agent-memory rebuild-index
```

Acceptance: archive flow preserves content. Remove archives then deletes. Heading rename preserves ID. Archived content not surfaced in normal fetches.

### M5 — Staging and Review

```text
Staging directory layout
TTL handling
Drift detection
agent-memory review (list, detail, --since)
agent-memory apply (with --commit, drift check)
agent-memory reject
agent-memory rebase
```

Acceptance: staged updates survive restarts. Drift detected on file change. Review output is actionable. Rebase re-resolves section IDs.

### M6 — Adapters

```text
install claude (with worked SKILL.md)
install codex
install cursor
install generic
Idempotent block markers in CLAUDE.md / AGENTS.md
```

Acceptance: install on a repo with existing CLAUDE.md preserves user content. Adapter SKILL.md drives agent calls in manual test.

### M7 — Git Integration

```text
init --with-merge-driver
agent-memory merge-driver (custom git merge tool)
agent-memory apply --commit
Optional pre-commit hook
```

Acceptance: two divergent branch edits to non-overlapping sections merge cleanly. Overlapping edits produce reviewable conflict block.

### M8 — Evaluation Harness

```text
Benchmark corpus (3 seeded repos)
Eval runner CLI
Adapter acceptance threshold checker
```

Acceptance: eval runs end-to-end on at least one agent host. Metrics reported. Adapter thresholds measurable.

---

## 32. Example Agent Workflow

### 32.1 Starting Work on a New Branch

Developer runs:

```bash
git checkout -b feature/auth-rotation
```

Agent (Claude Code, with `agent-memory` adapter installed) starts a session. SKILL.md tells it to call `memory.fetch_context` at session start.

```json
{
  "tool": "memory.fetch_context",
  "input": {}
}
```

Server resolves active branch as `feature/auth-rotation`. Returns:

- `local/current.feature-auth-rotation.md` (likely empty on first invocation).
- `local/current.shared.md` (notes that survive branch switches).
- `conventions.md` (build/test/branching rules).
- Compact `index.md` summary.

Agent sees the bootstrap and knows what's available.

### 32.2 Investigating a Module

Agent recognises it's about to work on auth code. Calls:

```json
{
  "tool": "memory.fetch_context",
  "input": {
    "query": "auth refresh token rotation",
    "scope": ["auth"],
    "budget": 8000
  }
}
```

Server returns:

- Active branch's `current.md` (unchanged from bootstrap).
- `modules/auth.md` with sections relevant to refresh tokens.
- Recent `decisions.md` entries about auth.
- Relevant `pitfalls.md` bullets.
- Section IDs included in each block so the agent can reference them.

Agent reads the pack, then begins editing source code under `internal/auth/`.

### 32.3 Recording Progress (Auto-Applied)

Agent updates branch-local state as it works:

```json
{
  "tool": "memory.propose_update",
  "input": {
    "intent": "update_current",
    "rationale": "Working on token rotation logic, refactored AuthRefreshService.",
    "operations": [{
      "operation": "replace_section_content",
      "path": "local/current.feature-auth-rotation.md",
      "section_id": "active-task",
      "content": "Refactoring AuthRefreshService to rotate refresh tokens on every use. Test coverage in internal/auth/refresh_test.go needs updating.\n"
    }]
  }
}
```

Returns `status: applied` immediately (current is auto-apply).

### 32.4 After Meaningful Change (Staged)

When the implementation is solid, agent records the durable change:

```json
{
  "tool": "memory.propose_update",
  "input": {
    "intent": "refresh_module",
    "rationale": "Token rotation behaviour finalized; module note must reflect new contract.",
    "operations": [{
      "operation": "replace_section",
      "path": "modules/auth.md",
      "section_id": "token-rotation",
      "content": "## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate on every successful use.\n\nContract: AuthRefreshService.refreshToken(userId) returns a new TokenPair and invalidates the previous refresh token.\n\nSources:\n- file: internal/auth/refresh.go\n- test: internal/auth/refresh_test.go\n"
    }],
    "sources": [
      {"type": "file", "ref": "internal/auth/refresh.go"},
      {"type": "test", "ref": "internal/auth/refresh_test.go"}
    ],
    "confidence": "confirmed"
  }
}
```

Returns:

```json
{
  "status": "staged",
  "staging_id": "2026-05-26T121500-auth-token-rotation",
  "review_command": "agent-memory review 2026-05-26T121500-auth-token-rotation"
}
```

Agent reports the staging ID to the developer.

### 32.5 Developer Review

Later, developer runs:

```bash
agent-memory review 2026-05-26T121500-auth-token-rotation
```

Sees rationale, sources, preview diff, schema validation result. Either:

```bash
agent-memory apply 2026-05-26T121500-auth-token-rotation
```

(applies, byte-spliced into `modules/auth.md`, FTS5 updated, durable file ready for git commit).

Or:

```bash
agent-memory reject 2026-05-26T121500-auth-token-rotation
```

(staging dir removed; developer can manually update memory if needed).

### 32.6 Branch Switch

Later, developer runs:

```bash
git checkout main
```

Next time agent calls `memory.fetch_context`, the server resolves `main` branch and returns `local/current.main.md` automatically. Feature branch's local state is preserved on disk for when the developer switches back.

---

## 33. UX Rules for Agents

Adapter SKILL.md / AGENTS.md should instruct agents:

1. Call `memory.fetch_context` (empty query) at session start.
2. Call `memory.fetch_context` with query+scope before touching unfamiliar modules.
3. Prefer one broad fetch over many narrow ones.
4. Do NOT read every memory file manually — use the tool.
5. Do NOT write directly to `.agent-memory/`.
6. Use `memory.propose_update` for any memory change.
7. Submit structured operations referencing section IDs, never unified diffs.
8. Do NOT choose dry-run/apply — the server decides.
9. Do NOT store secrets, credentials, customer data, or raw `.env` values.
10. Record durable knowledge only when confirmed and likely useful later.
11. Use `archive_section` or `remove_section` (which archives first) — never try to truly delete.
12. Keep `local/current.<branch>.md` short and oriented to current work.
13. Treat archived memory as historical, not current.
14. After branch switch, re-fetch context (the active branch's state has changed).
15. If `propose_update` returns `status: staged`, that's expected — not an error.

---

## 34. Open Questions

Most v0.3 open questions are now answered in this draft. Remaining:

1. Should `apply` auto-commit staged updates by default in CI mode? *(Lean: no, even in CI; commit should remain explicit.)*
2. How strict should stale-note warnings be in `status`? Current proposal: list, don't gate.
3. Should the first version support monorepo sub-projects (separate `.agent-memory/` per sub-package)? *(Lean: no; deferred to v0.5.)*
4. Should memory updates be reviewable through PRs in team usage (PR comments listing staged updates)? *(Lean: yes for v0.5 via GitHub Action.)*
5. Should the index include repository source files, or only memory files? *(Decision: only memory files. Source files are the agent's primary search target via Read/Grep; agent-memory is the *meta-layer*.)*
6. Should there be a trust boundary inside memory between human-written and agent-written content (e.g., per-section author metadata)? *(Lean: yes, but not in MVP — track via git blame for now.)*
7. How aggressive should auto-cleanup of orphan `local/current.*` be? Currently warn-only.
8. Should `agent-memory doctor` block writes if schema validation fails repo-wide? *(Lean: no, only warn.)*
9. Should adapters install version-pinned skill files or auto-update? *(Lean: version-pinned with explicit `install` re-run for upgrade.)*
10. What's the right cadence for benchmark eval against frontier agent versions? Weekly?

---

## 35. Future Work

### 35.1 v0.5

- Assisted compaction (server proposes, agent generates, human approves).
- File-change-aware context suggestions (watch git status, suggest fetches).
- Better stale-note detection (parse Sources, compare git mtime).
- GitHub Action: run validation + post PR comment with staged update summaries.
- PR-based review workflow for staged memory updates.
- Author tracking inside memory (human vs agent-written sections).
- Configurable secret rule sets.
- Interactive TUI for `review` selector (`charmbracelet/bubbletea`-based arrow-key navigation over staged updates).

### 35.2 v0.6

- Multi-repo workspace memory (cross-repo context for monorepos / polyrepo dev environments).
- Optional `current.task-<id>.md` overlay (per-task within a branch).
- Memory quality scoring.
- Conflict-resolution UI (CLI-based wizard for merge driver conflicts).

### 35.3 v1.0

- Stable memory format.
- Stable MCP contract.
- Robust adapter support with measured behaviour.
- Production-ready safety defaults.
- Proven team workflow.

### 35.4 Later

- Optional vector search (only if BM25 proves insufficient on benchmarks).
- Rust indexer (only if profiling justifies).
- Web dashboard.
- Sync backend.
- IDE extension.
- Memory diff review UI.

---

## 36. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| Agents ignore memory tools | High | Adapter instructions, SKILL.md, eval threshold gates |
| Memory bloat | High | Budgets, status warnings, archival compaction |
| Bad memory updates | High | Structured ops, schema validation, staging |
| Secret leakage | Critical | Reject-before-write scanning, allowlist for examples |
| Markdown roundtrip churn | High | Byte-preserving splice (§21) |
| Section identity drift | High | @id anchors (§12) |
| Concurrent write corruption | High | OS-level advisory lock; kernel auto-releases on process death (§11) |
| Drift false positives blocking apply | Medium | Per-section checksums; unrelated edits don't trigger drift (§16.4) |
| Local state Git conflicts | High | Per-branch local state + gitignore (§13) |
| Adapter fragmentation | Medium | Worked SKILL.md examples, eval thresholds |
| Search quality too weak | Medium | BM25 + scope + freshness; vector search deferred until proven necessary |
| Staging queue noise | Medium | Per-category approval, batch review, trivial auto-apply |
| Secret-scan false positives | Medium | Allowlist mechanism with reason |
| Overengineering | High | Three MCP tools, opinionated defaults, no vectors / no graph |
| Adapter behaviour regression after agent version change | Medium | Weekly eval against frontier versions |

---

## 37. Initial README Positioning

Sharp version:

> `agent-memory` is local context middleware for AI coding agents. One MCP call in, structured memory updates out. Branch-aware. Secret-safe. Byte-preserving. Three tools.

Sub-positioning:

> Safe, searchable, branch-aware project memory for coding agents. Markdown source of truth; SQLite shadow index; no cloud, no vectors, no agent-generated diffs.

Avoid:

> "Memory bank for agents." — Too close to naive Markdown-folder projects.

> "Knowledge graph for your repo." — Wrong category; overpromises.

---

## 38. Recommended First Implementation Path

```text
 1. Go CLI skeleton with cobra
 2. init: layout, manifest.yaml, schema.yaml, .gitignore, lock.json bootstrap
 3. Concurrency lock implementation (gofrs/flock + stale recovery)
 4. Byte-preserving Markdown engine
    - goldmark AST → byte offsets
    - splice primitive
    - @id anchor management
 5. Schema loader and validator
 6. SQLite FTS5 incremental index
 7. fetch CLI + memory.fetch_context MCP tool
 8. Branch-aware path resolver
 9. propose_update with operations (create, append_section, replace_section, append_to_section, replace_section_content)
10. Secret scanner with allowlist
11. Per-category approval routing
12. Staging engine with TTL, drift detection
13. review / apply / reject / rebase CLI
14. archive_section, remove_section, rename_heading
15. Claude Code adapter with worked SKILL.md
16. Codex, Cursor, generic adapters
17. Git merge driver
18. doctor, clean-local, clean-staging
19. Benchmark corpus + eval runner
20. README, install docs
```

Do NOT start with:

```text
vector search
Rust
web UI
cloud sync
complex graph model
more than three MCP tools
unified diff update API
agent-controlled dry-run/apply
auto-compaction
multi-repo workspace
```

---

## 39. Final Recommendation

Implement v0.4 MVP in Go. Use:

```text
github.com/modelcontextprotocol/go-sdk
modernc.org/sqlite (CGo-free)
yuin/goldmark (AST only)
gofrs/flock (advisory locks)
cobra (CLI)
yaml.v3 (config)
```

Keep the public MCP surface to three tools:

```text
memory.fetch_context
memory.propose_update
memory.status
```

The product is **local context middleware with byte-preserving, branch-aware, concurrency-safe memory**. Not a Markdown note-taking convention. Not a knowledge graph. Not a cloud product.

The sharp version:

```text
One context fetch in.
Structured memory operations out.
Stable section IDs.
Byte-preserving Markdown edits.
Per-category approval.
Branch-aware local state.
Single-writer concurrency.
Secrets rejected with allowlist mechanism.
Adapter quality measured by agent behaviour, not file generation.
No cloud. No vectors. No graphs. No agent-generated diffs.
```
