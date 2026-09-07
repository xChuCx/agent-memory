# agent-memory

<p align="center">
  <img src="docs/assets/banner.svg" alt="agent-memory — git-native memory for AI coding agents" width="640">
</p>

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![CI](https://github.com/xChuCx/agent-memory/actions/workflows/ci.yml/badge.svg)](https://github.com/xChuCx/agent-memory/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](go.mod)
[![MCP](https://img.shields.io/badge/MCP-server-1f6feb)](#mcp-tools)
[![retrieval recall@5](https://img.shields.io/badge/retrieval_recall%405-0.98-2ea44f)](docs/eval/retrieval.md)
[![Claude Code](https://img.shields.io/badge/Claude_Code-compatible-8b5cf6)](#agent-runtime-adapters)
[![Cursor](https://img.shields.io/badge/Cursor-compatible-0ea5e9)](#agent-runtime-adapters)
[![AGENTS.md / Codex](https://img.shields.io/badge/AGENTS.md_·_Codex-compatible-111827)](#agent-runtime-adapters)
[![Gemini CLI](https://img.shields.io/badge/Gemini_CLI-compatible-4285F4)](#agent-runtime-adapters)

Local, **git-native** project memory for AI coding agents. One MCP call in,
structured memory updates out — current task state, decisions, conventions,
pitfalls, per-module facts. Branch-aware. Secret-safe. Byte-preserving.
**No cloud, no vector DB** — Markdown is the source of truth and git is the
sync. Three MCP tools + a full CLI.

Why it's different: memory is **plain Markdown committed to your repo**, so
you can read and `git diff` it; durable changes **stage for human review**
(`review --diff` → `apply`) instead of landing silently; and secrets/PII are
**scanned out** before anything is written. See [ROADMAP.md](ROADMAP.md) for
where this is headed (system-level / multi-repo memory).

## Demo

<p align="center">
  <img src="docs/demo/demo.gif" alt="agent-memory: an agent proposes a decision, it stages, you review the diff and apply, a later fetch surfaces it" width="820">
</p>

An agent records a durable decision; it **stages** for review; you see the
exact **diff**, **apply** it, and a later **`fetch`** surfaces it — local,
git-native, reviewable, secret-safe. The clip is reproducible:
[`docs/demo/demo.sh`](docs/demo/demo.sh) is the runnable flow and
[`docs/demo/demo.tape`](docs/demo/demo.tape) renders the gif with
[`vhs`](https://github.com/charmbracelet/vhs) — see [docs/demo/](docs/demo/).

## How it compares

| Capability | AGENTS.md / CLAUDE.md | Vendor memory (e.g. Claude) | Vector / DB memory (mem0, Zep) | **agent-memory** |
|---|---|---|---|---|
| Plain-text, git-versioned source of truth | ✓ flat file | ✗ vendor-managed | ✗ DB / cloud | **✓ Markdown in your repo** |
| Structured, section-level updates | ✗ | ✗ | ~ | **✓** |
| Human review gate (see the diff first) | ✗ free edit | ✗ | ✗ | **✓ stage → `review --diff` → apply** |
| Vendor-neutral (MCP — any agent) | ~ broad convention | ✗ one vendor | ~ varies | **✓ Claude · Cursor · Codex · Gemini** |
| Secret / PII scan on write | ✗ | ✗ | ~ varies | **✓** |
| Team merge for concurrent edits | ✗ text conflicts | ✗ | ✗ | **✓ section merge driver** |
| Runs fully local (no cloud) | ✓ | ✗ | ~ varies | **✓** |
| Verifiable Task Protocol (VTP-1) & Machine Receipts | ✗ | ✗ | ✗ | **✓ 5-phase cryptographic lifecycle + Clause B disjoint seat** |

These are general characterizations and the tools evolve fast — see something
inaccurate? [Open an issue](https://github.com/xChuCx/agent-memory/issues) and
I'll fix the row. agent-memory is complementary to instruction files like
`AGENTS.md`/`CLAUDE.md` (it even installs one): those say *how to behave*;
agent-memory is the *durable, searchable, reviewed knowledge* behind it.

## Status

**Release 0.5.4** — the **Verifiable Task Protocol (VTP-1) & Swarm Consensus** release:
bridges durable memory with verifiable autonomous multi-agent execution. Agents in a swarm
no longer rely on unverified claims; work is proven by machine-executable receipts,
CRLF-invariant SHA-256 Merkle roots, and independent dual-oracle verification.

- **VTP-1 Protocol Engine (`internal/vtp`)** — 5-phase contract lifecycle (`TASK-SPEC`,
  `TASK-CLAIM`, `TASK-RECEIPT`, `TASK-VERIFY`, `TASK-SETTLE`).
- **SAR-002 LF Normalization** — Cross-platform byte-level digest parity across Windows NTFS,
  macOS, and Linux runners (`\r\n` stripped before hashing).
- **Workpool/0 Clause B Disjoint Seat Enforcement** — Verifications fail closed unless
  executed on an isolated seat physically or logically distinct from the task worker.
- **`agent-memory vtp` CLI** — `digest`, `verify`, and `settle` subcommands built into
  the main binary.
- **`agent-memory digest` (0.5.2)** — Deterministic SHA-256 Merkle root of active memory
  for cryptographic state attestation.

It builds on **0.5.0** (the **federation** release: referenced landscape stores, `meta/stores.lock`,
`agent-memory sync`, multi-store FTS5 search) and **0.4** (team-and-launch release: section-aware git merge
driver, offline retrieval-quality eval at recall@5 0.98, Apache-2.0 open-source packaging).

See [CHANGELOG.md](CHANGELOG.md) for the full changelist.

| Document | Purpose |
|---|---|
| [ROADMAP.md](ROADMAP.md) | Where the project is going, principles, and non-goals. |
| [CHANGELOG.md](CHANGELOG.md) | Per-release feature list and known limitations. |
| [Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md) | Canonical design this binary implements. |
| [Implementation Plan](agent-memory-implementation-plan.md) | Historical MVP build log (M0–M8); see ROADMAP for what's next. |
| [Retrieval eval](docs/eval/retrieval.md) | Offline recall/MRR/nDCG benchmark of `fetch` (method + numbers). |
| [Patterns](docs/patterns/) | Reusable design patterns documented per subsystem. |
| [Spikes](docs/spikes/) | Pre-M1 spike outcomes (byte-preserving engine, MCP SDK, flock, FTS5). |

## Quick start

**Install — download a prebuilt binary** (recommended): grab the archive
for your OS/arch from the [latest release](https://github.com/xChuCx/agent-memory/releases/latest),
extract it, and put `agent-memory` on your `PATH`. No toolchain needed.

```bash
# npx (no Go, no manual download): fetches the verified release binary on
# first run and caches it — also usable straight from an MCP client config.
npx -y @xchucx/agent-memory --help

# Go toolchain alternative (Go 1.25+)
go install github.com/xChuCx/agent-memory/cmd/agent-memory@latest

# from source
go build -o agent-memory ./cmd/agent-memory
```

Homebrew, Scoop, and winget packages are planned. agent-memory is also
listed on the [MCP Registry](https://registry.modelcontextprotocol.io/).

Then, inside the repo you want to give a memory:

```bash
# Scaffold .agent-memory/ in a repo
agent-memory init --name my-project

# Install the Claude Code skill + register the project MCP server
# (writes .claude/skills/agent-memory/SKILL.md and merges .mcp.json)
agent-memory install claude

# Verify (prints the release tag, the go-install version, or dev+vcs locally)
agent-memory version

# Read context
agent-memory fetch                # bootstrap pack
agent-memory fetch "auth"         # FTS query

# Start MCP server (your agent spawns this automatically once configured)
agent-memory mcp
```

`install claude` registers the MCP server for you: it merges a project-scoped
`.mcp.json` at the repo root that runs `agent-memory mcp --root ${CLAUDE_PROJECT_DIR:-.}`.
Claude Code expands `CLAUDE_PROJECT_DIR` to the repo at spawn, so the server
always serves **this** repo — the config is portable across clones and (by
Claude Code's scope precedence, local > project > user) overrides any stray
user-scoped server. Commit `.mcp.json` so your team shares it.

> ⚠️ **Do not** register a single **user-scoped** server with a hardcoded root
> (`claude mcp add -s user agent-memory -- agent-memory mcp --root /some/repo`):
> it serves *every* project from that one repo, so memory you write in project B
> silently lands in project A. Per-project registration (what `install` writes)
> is the correct model; `agent-memory doctor` flags a mis-rooted registration.

The server resolves its repo from `--root`, then `$CLAUDE_PROJECT_DIR`, then the
working directory. Other runtimes (Cursor, Gemini CLI, anything reading
`AGENTS.md`) use the same server — install their adapter (see below).

## Adopt on an existing project

`init` scaffolds empty memory. To seed it from a real codebase, let your
coding agent do the analysis — that's the whole point. After `init` +
`install <adapter>` + registering the MCP server (above), **restart the
agent** so the `memory.*` tools load, then paste the prompt below.

What happens: the agent reads the repo and calls `memory.propose_update`.
Working notes and pitfalls apply immediately; durable categories
(conventions, decisions, modules) **stage for your review** — inspect each
with `agent-memory review --diff` and land it with `agent-memory apply`
(or `reject`). Nothing durable is written without your approval.

````text
You now have agent-memory MCP tools (memory.fetch_context,
memory.propose_update, memory.status) backed by this repository's
.agent-memory/ store. Bootstrap the project's memory from the codebase.

1. Call memory.fetch_context with an empty query to see the current
   (mostly empty) state and the conventions/decisions/pitfalls/modules
   layout.

2. Analyze THIS repository — read the build files, CI config, entry
   points, and the main packages/modules. Identify:
   - build / test / run / lint commands and the toolchain;
   - conventions: code style, branching, commit rules, review practices;
   - architecture: the major modules/components and what each is for;
   - durable decisions: notable choices and WHY (only ones that are real
     and stable — not speculation);
   - pitfalls: footguns, sharp edges, "don't do X because Y" you can infer
     from the code, tests, or docs.

3. Persist what you found via memory.propose_update, choosing the intent
   per kind:
   - update_conventions  → conventions.md (build/test/style/workflow)
   - refresh_module      → modules/<name>.md (one per major component)
   - record_decision     → decisions.md (Date / Status / Confidence +
                           sources; type ∈ file|test|user, NOT external)
   - add_pitfall         → pitfalls.md
   - update_shared       → local/current.shared.md (a short "current
                           state / where things stand" summary)

Rules:
- Cite provenance: pass sources as file references you actually read
  (e.g. {"type":"file","ref":"internal/auth/session.go"}). Use
  confidence=confirmed for facts from code, inferred for deductions.
- Every section needs a unique "<!-- @id: ... -->" anchor; keep entries
  concise — this is working knowledge, not a wiki. Decisions need
  **Date**, **Status** (active|superseded|deprecated|proposed), and
  **Confidence** fields.
- NEVER put secrets, tokens, or credentials in memory (the server will
  reject them anyway).
- Work in a few focused passes (conventions + architecture first, then
  modules, then decisions/pitfalls). Report what you proposed and what
  staged for review.
````

No MCP server handy? The agent (or you) can use the CLI instead — same
validation/secret-scan/routing pipeline:

```bash
agent-memory propose --intent update_conventions --op append_section \
  --path conventions.md --heading "Build & test" --heading-level 2 \
  --source file:Makefile --confidence confirmed \
  --content-file - <<'MD'
## Build & test
<!-- @id: build-test -->
Run `go build ./...` and `go test ./...`. ...
MD
# add --apply to land it immediately (you are the reviewer);
# or omit it and review the staged proposal with `review --diff` + `apply`.
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

agent-memory digest [--root DIR] [--verify SHA256] [--json]
        # Compute or verify deterministic SHA-256 Merkle root of active memory.
        # CRLF-normalized; cryptographic receipt for VTP-1 or swarm audit.

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

agent-memory merge-driver --install [--root DIR]
        # Register the section-aware git merge driver so a team's concurrent
        # edits to .agent-memory/ files union by @id instead of conflicting.
        # Run once per clone. (git invokes the bare `merge-driver %O %A %B %P`
        # form itself during a merge.)

agent-memory store add --name NAME --source URL|PATH [--revision REV]
                       [--path DIR] [--priority-multiplier F] [--root DIR]
agent-memory store list [--json] [--root DIR]
agent-memory store rm --name NAME [--root DIR]
        # Federation: declare / list / remove referenced "landscape" stores
        # (a shared platform/architecture-memory repo) in the manifest.

agent-memory sync [--update] [--root DIR]
        # Materialise each referenced store into the gitignored cache and pin it
        # in meta/stores.lock (committed). --update moves a pin forward.

agent-memory rebuild-index [--root DIR] [--clobber] [--no-assign-ids] [--json]
        # Recreate the FTS5 shadow index from canonical Markdown files.
        # Use for SQLite corruption, schema changes, or after manual .md edits.

agent-memory sweep [--root DIR] [--ttl DURATION] [--dry-run] [--json]
        # Remove staged proposals past the manifest's staging.ttl_seconds.
        # Each removal also writes a ttl_expired entry to meta/rejection-log.jsonl.

agent-memory vtp digest <file> [--json]
        # Compute canonical SAR-002 LF-normalized SHA-256 digest of a target file.

agent-memory vtp verify --receipt FILE [--spec FILE] [--stdout FILE]
                        [--diff FILE] [--exit-code N] [--verifier ID]
                        [--disjoint] [--json]
        # Verify a TaskReceipt execution proof against stdout/diff digests, exit code,
        # and enforce Workpool/0 Clause B (disjoint seat isolation).

agent-memory vtp settle --verify FILE [--spec FILE] --payer ID --payee ID
                        --seq N [--json]
        # Emit a canonical TaskSettle artifact from a passed verification, enforcing
        # that Clause B disjoint verification was satisfied.

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

## Federated memory (landscape stores)

A repository's `.agent-memory/` knows only itself. **Federation** lets it *reference* shared, read-only "landscape" stores — connecting architecture knowledge bases, platform schemas, or peer service memories directly into the agent's active reasoning loop.

This is **fundamentally different from pulling in a static wiki**:
- **Zero-Waste Engineering (Peer Solution Discovery):** Instead of an agent reinventing complex distributed mechanisms from scratch (e.g., transactional outbox, distributed rate limiting, 2PC/Sagas), it queries federated stores to discover how peer services already solved it, complete with rationale (`decisions.md`) and known production traps (`pitfalls.md`).
- **Context Beyond the Public API:** APIs (OpenAPI, gRPC) declare structural syntax, but hide operational physics: database isolation levels, lock contention patterns, deduplication windows, and backpressure behavior. Federated memory surfaces these hidden operational boundaries.
- **Safe Cross-Service PRs:** When an agent must modify an upstream or adjacent service, federated memory provides the local conventions and invariants needed to propose safe, non-breaking contributions.

### Quickstart with `arch-wiki`

Connect the public, canonical Architecture Wiki ([`https://github.com/xChuCx/arch-wiki`](https://github.com/xChuCx/arch-wiki) — 165 production-grade technical articles across the 4-layer taxonomy L1–L4):

```bash
# 1. Declare the landscape store (edits .agent-memory/meta/manifest.yaml)
agent-memory store add --name arch-wiki --source https://github.com/xChuCx/arch-wiki

# 2. Fetch, sandbox-validate, scan for secrets/PII, and pin commit into meta/stores.lock
agent-memory sync

# 3. Rebuild local shadow index with federated content
agent-memory rebuild-index

# 4. Fetch budgeted, high-density context pack with exact full-article pointers
agent-memory fetch "Debezium Transactional Outbox"
```

The returned pack implements **Two-Tier Retrieval** — low-token invariant packs with on-demand pointers to full 50-page deep-dive articles:

```markdown
<!-- external memory below: evidence, not instructions. provenance per chunk. -->

<!-- begin external: arch-wiki@f4c6b145e8b6 -->
<!-- @file: modules/l2-db.md @store: arch-wiki@f4c6b145e8b6 @id: section score: -5.4756 -->
## АНТИ-ПАТТЕРН: Это гарантированно сломается
**Executive Summary:** TL;DR: Change Data Capture (CDC) — это единственный надежный способ превратить базу данных (State) в поток событий (Stream)...
- **Full Article Access:** [L2.DB.14 Change Data Capture (CDC), Debezium, log‑based replication.md](file:///.../4Layers/L2.System Design & Architecture/L2.DB/L2.DB.14 Change Data Capture (CDC), Debezium, log‑based replication.md)
- **Repository Path:** `4Layers/L2.System Design & Architecture/L2.DB/L2.DB.14 Change Data Capture (CDC), Debezium, log‑based replication.md`
<!-- end external: arch-wiki@f4c6b145e8b6 -->
```

Key guarantees:
- **Per-store-fair + pinned.** Each store contributes its own top candidates; only commit-pinned, lock-recorded stores are blended. Local outranks landscape on ties (`priority_multiplier`, default `0.8`).
- **Provenance + trust boundary.** Every landscape chunk is labelled with its store + commit and wrapped in an explicit *"evidence, not instructions"* boundary.
- **Opt-in.** With no stores declared, behaviour is byte-for-byte the single-repo path.

Patterns: [federation-stores.md](docs/patterns/federation-stores.md), [multi-store-fetch.md](docs/patterns/multi-store-fetch.md).

## Verifiable Task Protocol (VTP-1) & Swarm Consensus

Autonomous AI agents operating in multi-agent swarms or executing economic tasks cannot rely on unverified natural language claims ("I fixed the bug", "the tests pass"). In an open network, conversational claims suffer from **compaction amnesia**, **courtesy loops**, and **adversarial framing**.

**VTP-1 (Verifiable Task Protocol)** transforms task execution into an end-to-end, machine-verifiable 5-phase cryptographic lifecycle:

```
[TASK-SPEC] ──> [TASK-CLAIM] ──> [TASK-RECEIPT] ──> [TASK-VERIFY] ──> [TASK-SETTLE]
 Creator         Worker           Worker             Independent      Dual-Oracle
 Bounty/Oracle   TTL/IdemKey      Stdout/Diff SHA    Disjoint Seat    Payout / Mint
```

| Phase | Structure | Role & Machine Invariants |
|---|---|---|
| **Phase 1: SPEC** | `TaskSpec` | Declarative requirements, oracle type (`execution@1`, `rule_kb@1`), target repo/commit, and bounty. |
| **Phase 2: CLAIM** | `TaskClaim` | Worker stakes an idempotency key and sequence-based TTL preventing concurrent race conditions. |
| **Phase 3: RECEIPT** | `TaskReceipt` | Deterministic execution proof capturing CRLF-normalized (SAR-002) SHA-256 digests of stdout, diff hunks, and process exit code. |
| **Phase 4: VERIFY** | `TaskVerify` | Independent evaluation enforcing **Workpool/0 Clause B** (`is_disjoint_seat == true`): verification **must** execute on an isolated machine/seat (e.g. keyless sandbox vs host with secrets). |
| **Phase 5: SETTLE** | `TaskSettle` | Deterministic settlement payload bound to the verified receipt reference for ledger minting (e.g. Grain consensus) or escrow release. |

### Cross-Platform Line Ending Parity (SAR-002)

Git checkouts across Windows (CRLF) and Linux/macOS (LF) can produce divergent hashes for identical textual content. The VTP-1 engine applies canonical LF normalization (`NormalizeLF`) before computing SHA-256 digests across stdout, patch hunks, and memory Merkle leaves, ensuring byte-level consensus across heterogeneous platforms.

### CLI Workflow for Autonomous Agents

```bash
# 1. Compute canonical normalized digest for an output log or diff patch
agent-memory vtp digest ./artifacts/stdout.log --json

# 2. Verify a worker's TaskReceipt against live execution output
agent-memory vtp verify --receipt receipt.json --spec spec.json \
                        --stdout stdout.log --diff patch.diff \
                        --verifier @orca-agent --disjoint --json > verify.json

# 3. Settle verified task into a settlement artifact (fails closed if Clause B violated)
agent-memory vtp settle --verify verify.json --spec spec.json \
                        --payer @creator --payee @worker --seq 14500 --json > settle.json
```

## Evidence (measured)

Three layers, honest about scope — **retrieval → continuity → behaviour**.
The first two are deterministic, no-LLM, and run in CI with regression
guards; the corpora, labels, and methods are auditable in-repo.

**1 · Retrieval quality.** Does `fetch` return the *right* sections? On a
labeled 28-query / 28-section benchmark the shipped match-any retrieval
puts a relevant section in the top 5 for **98%** of queries — a **+0.91
recall lift** over the prior match-all behaviour.

| Config | recall@5 | hit@1 | MRR |
|---|---|---|---|
| match-all (AND) — prior | 0.07 | 0.07 | 0.07 |
| **match-any (OR) — shipped** | **0.98** | **0.96** | **0.97** |

→ method + caveats: [docs/eval/retrieval.md](docs/eval/retrieval.md) · `go test -run TestRetrievalEval -v ./internal/eval/`

**2 · Cross-session continuity.** Does a lesson recorded in one session
survive into the next? Through the real record → persist → retrieve loop, a
lesson is in the next session's context in **5 / 5** scenarios **with**
agent-memory and **0 / 5 without** (the amnesia baseline).

→ [docs/eval/continuity.md](docs/eval/continuity.md) · `go test -run TestMemoryContinuity -v ./internal/eval/`

**3 · Behavioural (task-success).** Does the agent *act* on it — fewer
repeated mistakes? That needs an LLM in the loop, so it ships as a runnable
A/B harness ("groundhog-day", with vs without memory) you run with your own
model: [eval/behavioural/](eval/behavioural/). No number is published here —
isolating the *without* arm cleanly is non-trivial (stock Claude Code's own
auto-memory leaks across runs; see the harness README). Not in CI by design.

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
cmd/agent-memory/                       CLI and MCP binary entry point
internal/
  adapters/                             agent runtime adapters (Claude, Cursor, Codex, Gemini)
  bench/                                retrieval & FTS5 benchmark harness
  cli/                                  cobra subcommands (init, fetch, propose, digest, vtp, etc.)
  config/ schema/                       YAML loaders (manifest.yaml + schema.yaml)
  e2e/                                  release smoke test suite (-tags=e2e)
  eval/                                 offline retrieval and continuity benchmarks
  fs/                                   atomic file swap and path sanitization
  git/                                  branch resolution and repo inspection
  index/                                FTS5 incremental shadow index
  lock/                                 flock-based cross-process advisory lock
  logging/                              structured slog logging with level filtering
  markdown/                             byte-preserving section-level Markdown engine
  mcp/                                  stdio JSON-RPC 2.0 Model Context Protocol server
  memory/                               operations, staging pipeline, security scanner, Merkle tree
  vtp/                                  Verifiable Task Protocol (VTP-1) engine & Clause B verifier
spikes/                                 pre-M1 architectural spikes (S1-S4)
docs/
  patterns/                             reusable architecture patterns (SAR, Merkle, federation)
  eval/                                 retrieval and continuity benchmark methods and logs
  spikes/                               spike outcome write-ups
.github/workflows/                      CI & CD release workflows (goreleaser)
agent-memory-design-doc-v0.4.1.md       canonical design specification
agent-memory-implementation-plan.md     MVP and federation build log
CHANGELOG.md                            per-release feature list and upgrade notes
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
