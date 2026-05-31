# Changelog

All notable changes to **agent-memory** are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.0] — 2026-05-31

The team-and-launch release: agent-memory is open-source-ready and safe to
share across a team. Highlights — a section-aware **git merge driver**
(M7) so concurrent edits to `.agent-memory/` union by section instead of
conflicting; an offline **retrieval-quality eval** (recall@5 0.98,
CI-guarded) so the search has a defensible number; Apache-2.0 licensing
and open-source packaging (SECURITY/CONTRIBUTING/CoC, ROADMAP, badges,
reproducible demo); and the Go module path corrected to
`github.com/xChuCx/agent-memory` so `go install …@latest` resolves.

### Added

- **Section-aware git merge driver (M7) — team sharing.** `.agent-memory/`
  is shared through git, but two branches appending to the same memory file
  (a decision, a pitfall) used to collide on line-based merge. The new
  `agent-memory merge-driver` merges by section (`@id`): both-append →
  clean **union**; one-sided edit → taken; both edit the same section →
  a scoped `@merge-conflict` block for review; delete-vs-edit keeps the
  surviving section. Byte-preserving. `agent-memory merge-driver --install`
  registers it (committed `.agent-memory/.gitattributes` + per-clone git
  config); `memory.status` reports `merge_driver_installed`. Verified
  against real `git merge`. See
  [docs/patterns/merge-driver.md](docs/patterns/merge-driver.md).

- **Offline retrieval-quality eval** (`internal/eval`). A deterministic,
  no-LLM benchmark: 28 natural-language queries over a 28-section labeled
  corpus, reporting recall@5 / hit@1 / MRR / nDCG@5. The shipped match-any
  retrieval scores recall@5 **0.98** (hit@1 0.96, MRR 0.97) — a **+0.91**
  recall lift over the prior match-all behaviour. Runs in CI with
  regression floors. Method + caveats (scope is retrieval recall, not
  agent task-success) in [docs/eval/retrieval.md](docs/eval/retrieval.md).
- **Open-source hygiene**: `LICENSE` (Apache-2.0), `SECURITY.md`,
  `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, issue/PR templates, and a
  forward-looking [`ROADMAP.md`](ROADMAP.md). The implementation plan is
  relabeled a historical build log.
- **README badges + reproducible demo.** Shield badges (license, CI, MCP,
  retrieval recall@5, runtime compatibility) and a `docs/demo/` showcase: a
  tested `demo.sh` flow and a `vhs` tape that renders the README gif
  (propose → stage → `review --diff` → apply → fetch).

### Changed

- **Module path renamed** `github.com/agent-memory/agent-memory` →
  `github.com/xChuCx/agent-memory` so it matches the repository and
  `go install github.com/xChuCx/agent-memory/cmd/agent-memory@latest`
  resolves. No behavioural change; release binaries were unaffected.

## [0.3.0] — 2026-05-29

The completeness-and-polish release. It closes the remaining design-doc
gaps from the v0.2.0 audit and hardens the everyday workflow — much of the
polish surfaced by dogfooding agent-memory on its own repository.

Highlights: the third MCP tool `memory.status`; M4 archival operations
(`archive_section` / `remove_section` / `rename_heading`) with a
server-maintained `index.md`; the secret + PII hardening layer with
allowlist limits; real per-section schema validation; structured `slog`
logging (stderr-only, secret-safe); smarter retrieval (Jaccard dedup, the
§20.4 ranking signals, OR-match recall, crash-safe FTS queries); and a
fuller CLI — `propose` (create proposals without an MCP server),
`review --diff`, and staging-id prefixes + `--latest`. Tag-driven
goreleaser publishes the cross-platform binary matrix.

### Added

- **`agent-memory propose` — CLI write path.** Create proposals without an
  MCP server, through the same `memory.ProposeUpdate` pipeline (validation,
  secret/PII scan, provenance, routing). Flags cover the common
  single-operation case (`--intent --op --path --section-id --heading
  --heading-level --content/--content-file/- --source type:ref
  --confidence --rationale …`); `--from-json -` takes a full multi-operation
  `ProposeRequest` (rejecting unknown fields). `--apply` immediately applies
  a result that would otherwise stage — the developer running the command is
  the reviewer — by composing the existing `ApplyStaged` (drift re-check +
  index + git auto-stage); the agent-facing MCP path keeps its review gate.
  `--json` output; non-zero exit on rejection. See
  [docs/patterns/cli-propose.md](docs/patterns/cli-propose.md).

- **Staging-ID prefix matching + `--latest`.** `review`, `apply`,
  `reject`, and `rebase` no longer require the full 30-character
  staging id. `memory.ResolveStagingID` accepts a full id, any unique
  prefix (Git-style), or the `--latest` flag (newest staged proposal).
  Ambiguous prefixes error with the list of candidates;
  no-match/empty-queue errors with `no matching staged proposal`. The
  E2E smoke test now applies via `--latest`.

- **Structured logging via `slog`.** A new `internal/logging` package
  centralises logger construction. Both transports log to **stderr**
  only — stdout stays reserved for the MCP JSON-RPC channel and CLI
  command output — and logging is **quiet by default** (WARN), opt-in
  via the CLI `--log-level debug|info|warn|error` flag or the
  `AGENT_MEMORY_LOG` environment variable.
  - The orchestrator emits a single deferred terminal-outcome line per
    operation (`propose_update` / `apply` / `rebase`) plus a
    served-summary line for `fetch_context`. Normal outcomes log at
    INFO/DEBUG; a `secret_detected` / `pii_detected` rejection logs at
    WARN.
  - **Secret-safe by construction and by test:** logs record stable
    reason codes and counts, never matched credential/PII bytes, and the
    raw fetch query is deliberately never logged. `redaction_test.go`
    captures emitted records and asserts the sensitive bytes are absent.
  - `memory.status` stays read-only and unlogged. See
    [docs/patterns/structured-logging.md](docs/patterns/structured-logging.md).

- **Near-duplicate suppression in `fetch_context` (§15.1 / §20.5).** The
  search-based context pack now collapses semantically overlapping
  sections: after ranking and before budget enforcement, a candidate
  whose token-set Jaccard similarity to an already-accepted (higher-
  ranked) section exceeds `0.85` is dropped and reported `omitted` with a
  `near-duplicate of higher-ranked section` reason. Keeps the pack from
  paying budget twice for one idea. Dependency-free set Jaccard over
  lowercased word tokens; threshold is a constant, matching the ranking
  multipliers. See
  [docs/patterns/context-pack-dedup.md](docs/patterns/context-pack-dedup.md).

- **Fetch ranking signals wired (§20.4).** Three previously-unimplemented
  signals now re-rank search hits:
  - **active-branch reference ×1.3** — a section whose body mentions the
    current branch (suppressed for generic `main`/`master`/… branches).
  - **decision/pitfall → changed file ×1.4** — a `decisions.md` or
    `pitfalls.md` section citing a file with uncommitted changes
    (`git status`), surfacing prior art for what you're editing.
  - **low-confidence ×0.8** — a section declaring `Confidence:`
    inferred/stale/unknown is downweighted vs `confirmed`.

  Content-level signals read the indexed section body, now returned as
  `index.SearchResult.Content`; `ActiveBranch` + `ChangedFiles` (new
  `git.ChangedFiles`) are resolved by the CLI/MCP caller and passed in
  `FetchDeps`. Multipliers are package constants. See
  [docs/patterns/ranking-signals.md](docs/patterns/ranking-signals.md).

- **`review <id> --diff`.** Shows a unified diff of each staged file
  against the current on-disk version — exactly what `apply` would change
  — so a proposal can be inspected before approval without dumping the
  whole file (`--show` still does that). Dependency-free line-level LCS
  unified diff (`internal/cli/diff.go`); a missing target (create_file)
  diffs against empty. Surfaced in both human and `--json` output.

### Changed (contract)

- **propose_update output shapes (§15.2).** Applied responses now carry
  `applied_at`, `affected_sections`, `index_updated`, `warnings`;
  staged responses carry `staging_ttl_seconds`, `human_approval_required`,
  `review_command`. Surfaced through both the Go API and the MCP tool.
- **`confidence` defaults to `inferred`** when omitted (§15.2), recorded
  in provenance checks and staged proposals.
- **`create_file if_exists=replace` forces staging** on durable
  (git-tracked) categories (§15.3), even when the category is set to
  auto-apply. Ephemeral local categories (current/sessions) keep
  auto-apply — wholesale replace is their normal mode.

- **Server-maintained `index.md` regeneration.** The schema reserved
  `index.md` as `server_managed` since v0.1, but the server never
  rewrote it after `init` wrote a one-time stub. Now the server
  regenerates it (design §10.1) as a side effect of every durable
  write — closing the last red acceptance-gate gap.
  - `memory.BuildIndexContent` produces the §10.1 routing structure
    (Always-include / Topic map / Archive / Freshness). The topic map
    tallies decisions by Status (e.g. "3 active, 2 superseded"), counts
    pitfall entries, and lists modules; the archive line counts
    archived contexts.
  - **Deterministic** — no wall-clock in the body. `RegenerateIndex`
    writes only when the content actually differs, so an apply that
    doesn't change the summary leaves `index.md` untouched (no git
    churn, stable tests).
  - Regenerated on `init`, on every apply (`applyImmediately` +
    `ApplyStaged`), and by `rebuild-index`. Best-effort in the apply
    paths — a regeneration failure never rolls back the durable write.
  - When it changes and is git-tracked, `index.md` joins the git
    auto-stage batch and is re-indexed into the FTS shadow.
  - Freshness/stale tracking remains a documented placeholder until
    per-section freshness markers (§20.3) land.
  - Documented in [docs/patterns/index-regeneration.md](docs/patterns/index-regeneration.md).

- **M4 — Archival operations: `archive_section`, `remove_section`,
  `rename_heading`.** Completes the eight-operation set from design
  §15 (was five) and closes the Release 0.2 acceptance gate.
  - `archive_section` (§15.8) copies a section to a new `archive/`
    file and replaces the source section with a stub. `remove_section`
    (§15.9) is archive-first removal: copy to `archive/`, then splice
    the section out of the source entirely. `rename_heading` (§15.10)
    changes a heading's text (and level, constrained to ±1) while
    preserving the `@id` anchor and the body.
  - **Multi-file operations.** archive_section / remove_section are
    the first ops that write to two files (source + new archive). A
    new optional `ExtraFileProducer` interface
    (`ExtraFiles(src) ([]ExtraFile, error)`) lets them produce the
    archive file without changing the five existing single-file ops.
    The orchestrator validates each extra (path, category, markdown,
    secret/PII scan, not-already-exists) and merges it into the
    staging/apply file set.
  - **Write-once enforcement.** Archive files cannot be modified once
    they exist: a mutating op on an existing `archive/` file →
    `write_once_violation`; an archive destination that already exists
    → `archive_exists`. The `require_file_absent` drift target
    re-checks at apply time.
  - **Always-stage.** archive_section and remove_section are forced to
    stage regardless of the intent's routing (durable + destructive →
    human review). The `routing.reason` records the override.
    rename_heading follows normal intent routing.
  - Adapter docs (SKILL.md, AGENTS.md, GEMINI.md, cursor MDC) gain the
    three operations with the write-once note. E2E smoke test does a
    full archive_section stage→apply round-trip through MCP.
  - Documented in [docs/patterns/archival-operations.md](docs/patterns/archival-operations.md).
- **`memory.status` — the third MCP tool.** Completes the design §15
  three-tool surface (`fetch_context` + `propose_update` +
  `status`). Returns the full §15.11 shape: per-kind file counts
  (durable / archive / sessions / local-current), index + current-state
  sizes, orphan branch-local files, pending staged proposals each with
  age / TTL-remaining / **drift status** (same `CheckDrift` machinery
  `apply` uses), plus security / git / lock posture blocks.
  - New shared `internal/memory.BuildStatus` + `MemoryStatus` type;
    both the CLI `status` subcommand and the MCP tool render from it,
    so the two transports return identical structured data.
  - `agent-memory status` output expanded: the §15.11 blocks now show
    in both `--json` (flattened into the report object) and the human
    renderer (Files / Sizes / Staged updates / Security / Git / Lock
    sections), on top of the existing per-category counts.
  - `internal/git.ListLocalBranches` added to detect orphan
    `local/current.<slug>.md` files whose branch no longer exists.
  - Conservative approximations documented inline for fields awaiting
    future mechanisms: `stale_notes` (freshness tracking), `security.
    last_secret_scan` ("n/a" until a scan log is persisted),
    `lock.stale_recoveries_last_24h` (kernel-managed locks need no
    recovery counter yet).
  - Adapter docs (SKILL.md, AGENTS.md, GEMINI.md, cursor MDC) updated:
    the intro now lists three tools; Claude's quick-reference table
    gains a `memory.status` row.
  - E2E smoke test asserts all three tools appear in `tools/list` and
    drives `memory.status` through the MCP transport.
- **Section schema maturity.** Real `SectionSchema` lands for the
  `decisions` category in `DefaultSchema()`: three required fields
  per section — Date (ISO 8601), Status (enum: active / superseded /
  deprecated / proposed), Confidence (enum: confirmed / inferred /
  user-provided). The validator that was wired but dormant in v0.1.0
  now does real work for new + modified decisions.
  - **Parser handles markdown emphasis.** `**Date:** 2026-05-27`,
    `*Date:* 2026-05-27`, and plain `Date: 2026-05-27` all parse
    identically. Bullet detection still works (mandatory space after
    the marker distinguishes `* foo` from `**bold**`).
  - **Affected-only validation.** The orchestrator validates only
    sections this proposal *created or modified*, using a
    `directBody` comparison (heading + immediate prose, excluding
    nested descendants). Legacy decisions written before the schema
    landed stay valid until edited; an `append_section` that adds a
    child under a parent doesn't trigger spurious "parent's full
    range changed" re-validation.
  - **Per-violation identity in error messages.** Rejection messages
    now name the offending section: `section @id=use-postgres:
    required field missing` so the agent can fix the right one when
    multiple sections are involved.
  - Adapter docs (SKILL.md, AGENTS.md, GEMINI.md, cursor MDC) updated
    to use lowercase enum values (`Status: active`, not `Active`).
  - Migration: forward-only safe. Existing v0.2.0 repos with their
    own `meta/schema.yaml` keep working unchanged. Fresh
    `agent-memory init` writes the new defaults. Existing repos
    opt in by adding `section_schema:` blocks to their schema.yaml.
  - Documented in [docs/patterns/section-schema.md](docs/patterns/section-schema.md).
- **Security hardening — allowlist limits + PII detection.** Two new
  guardrails on top of v0.1.0's regex + entropy secret scanner:
  - **Allowlist size limits** (`manifest.security.allowlist_limits`):
    `max_bytes_per_file` (default 1024), `max_regions_per_file`
    (default 10), `max_bytes_per_region` (default 512). Prevents
    `<!-- @secret-scan: allow reason="..." -->` from being abused to
    wrap multi-KB regions around real credentials. Field = 0 means
    "disabled" (escape hatch for projects with legitimate need).
    New reject reason: `allowlist_limit_exceeded`.
  - **PII detection** (`manifest.security.pii_scan` default ON):
    SSN-shape (`\d{3}-\d{2}-\d{4}`) and credit-card with Luhn
    validation. Both are extremely rare in legitimate technical
    content — Luhn gate drops the 13-19-digit false-positive rate
    by an order of magnitude. New reject reason: `pii_detected`.
  - **Email detection** (`manifest.security.pii_scan_email` default
    OFF): opt-in because emails legitimately appear in
    documentation. When enabled, allowlist regions can exempt
    specific addresses.
  - `ClassifyFindings` merges mixed credential + PII results into a
    single reason: any credential present → `secret_detected`; only
    PII → `pii_detected`. Mirror reasons added on the rebase path:
    `rebase_pii_detected` alongside existing `rebase_secret_detected`.
  - Documented in [docs/patterns/security-hardening.md](docs/patterns/security-hardening.md).
- **Release infrastructure** ([goreleaser](https://goreleaser.com/) +
  GitHub Actions workflow). Pushing a `v*` tag triggers
  `.github/workflows/release.yml`, which builds binaries for
  Linux/macOS/Windows × amd64/arm64 (static, CGo-free thanks to
  modernc.org/sqlite), archives them with README + CHANGELOG (tar.gz
  / .zip per OS), generates SHA-256 checksums, and publishes a
  GitHub Release with everything attached. Release notes are
  auto-generated from git history (docs/test/chore commits filtered
  out) with a header pointing to the curated CHANGELOG.md.

  `ProgramVersion` was switched from `const "0.X.Y"` to
  `var = "dev"` so source builds (`go build`) identify as `dev`
  while goreleaser stamps the actual tag via `-ldflags
  '-X .../cli.ProgramVersion=v0.X.Y'`. No more per-release source
  bump needed — the tag is the source of truth.

  CI gains a `goreleaser-check` job that validates
  `.goreleaser.yml` syntax on every PR + main push, so a broken
  config surfaces before it breaks a real tag push.

### Changed

- **`fetch` now matches ANY query term (OR), not all (AND).** Multi-word
  natural-language queries ("how does token refresh work") previously
  required every word to co-occur in one section and so matched almost
  nothing — the main recall problem dogfooding surfaced. `sanitizeFTSMatch`
  now OR-joins the quoted terms; BM25 ranks the partial matches (more / rarer
  terms → higher) and the budget keeps the top. The bootstrap current-state
  files are still prepended first, so OR can't crowd them out.

- `internal/cli.ProgramVersion` is now `var` (was `const`).
  Pre-built v0.2.0 binaries still report `0.2.0` because the
  ldflags stamp is applied per build; nothing changes for users.

### Fixed

- **`install <adapter> --root <relative>` failed** with
  `WriteAtomic: path must be absolute` (seen as
  `install gemini: ... write GEMINI.md: ... "GEMINI.md"`). A relative
  `--root` was passed straight to the adapter, which handed WriteAtomic a
  relative path. `runInstall` now resolves `--root` to an absolute path
  for every project-local adapter before dispatch. Regression test
  added (`TestRunInstall_RelativeRootResolvedToAbsolute`).

- **`fetch` crashed on queries with FTS5 metacharacters.** A query
  containing a hyphen (`auto-apply`), a reserved word (`AND`/`OR`/`NEAR`),
  a column filter (`x:y`), or an unbalanced quote was passed verbatim to
  the FTS5 `MATCH` parser and failed with `SQL logic error` /
  `no such column`. The query is now treated as natural language:
  `Search` tokenizes it and quotes each term (`sanitizeFTSMatch`), so
  metacharacters match literally. A query with no alphanumeric content is
  treated like an empty query. Found by dogfooding.

- **`append_section` / `append_to_section` no longer abut the next
  heading.** Inserting at the section's `ByteEnd` placed the new text after
  the section's trailing blank line, detaching it from the body and gluing
  it to the following heading (visible in `review --diff` dogfooding). A
  new `spliceAppend` helper inserts after the last non-blank line and
  re-emits a clean seam — one blank line before the next heading (single
  trailing newline at EOF), byte-preserving elsewhere. Found by dogfooding.

## [0.2.0] — 2026-05-27

Quality-of-life release on top of v0.1.0's Core Contract: git
auto-stage, staging TTL sweeper + audit log, three new agent-runtime
adapters, full-tree index rebuild, drift-recovery rebase, and a
benchmark harness. Six closed milestone kits; one stretch (git merge
driver) deferred to Release 0.3.

### Added

- **M8 — Benchmark harness.** `internal/bench/` is the new home of
  reproducible end-to-end benchmarks. A deterministic fixture
  generator (small / default / large sizes) builds a realistic
  `.agent-memory/` tree once; benchmarks for `FetchContext`,
  `ProposeUpdate` (apply / stage / session_log paths), and
  `RebuildAll` run against that fixture. Plus per-package
  hot-path benchmarks: `ParseSections` / `Splice` (markdown),
  `Scan` clean/with-key/with-allowlist (memory), `Search` over
  small/medium/large indices + `UpsertSections` (index).

  Twenty-one benchmarks total. `scripts/bench.sh` is the runner
  with consistent flags (`-bench=. -benchmem -count=3 -run=^$`);
  `benchstat` integration is documented in
  [docs/bench-harness.md](docs/bench-harness.md) along with the
  baseline numbers from a local Windows / NVMe run and an
  interpretation guide (why each path costs what it does).

  Not in scope for M8: CI-gated regression detection. Benchmarks
  run on-demand; baseline pinning + tolerance policy is M8 batch 2.
- **M7 — `rebase` CLI.** Recovery path for staged proposals that hit
  `target_drift` on apply (someone edited the base file between
  stage and apply). `agent-memory rebase <id> [--force] [--json]`
  re-runs each operation's Plan against the now-current disk bytes
  and writes refreshed staged files + target hashes, so the next
  `apply` succeeds.
  - Classifies each drift as **hard block** (file/section gone —
    rebase impossible) vs **soft drift** (section still resolves,
    only its hash differs — rebase-able with `--force`).
  - `--force` is mandatory for soft drifts: it's the user's
    explicit ack that "the new base content is acceptable as the
    re-planning input". Without it, rebase prints a diagnostic and
    exits non-zero.
  - Re-splice runs the **same** validation pipeline as
    `propose_update`: ValidateMarkdown + secret scan. A malicious
    or accidental edit that injects a credential into the base
    is caught here (`reason: rebase_secret_detected`); no staged
    files are written.
  - Provenance and routing are NOT re-checked — those were locked
    in at original stage time.
  - Does NOT apply to disk, reset `staged_at`, or touch the
    rejection audit log. Pure stage-area recovery.
  - Documented in [docs/patterns/rebase.md](docs/patterns/rebase.md).
- **M6 batch 2 — Three new agent-runtime adapters.** `agent-memory
  install` now ships four targets:
  - `claude` (existing) → `.claude/skills/agent-memory/SKILL.md`,
    Claude Code skill format with YAML frontmatter.
  - `cursor` → `.cursor/rules/agent-memory.mdc`, Cursor IDE MDC rule
    with description-based matching. Project-local AND `--user-global`
    (`~/.cursor/rules/`).
  - `agents` → `AGENTS.md` at the repo root. Industry-broad plain
    markdown convention read by OpenAI Codex CLI, Cursor's agent
    mode, Sourcegraph Cody, and others. **Project-local only** —
    `--user-global` is rejected because there's no agreed home-dir
    location.
  - `gemini` → `GEMINI.md` at the repo root. Gemini CLI long-term
    project memory. Project-local only.

  Each adapter follows the contract documented in
  [docs/patterns/adapter-installation.md](docs/patterns/adapter-installation.md):
  embedded asset, `Install(Options) (*Result, error)`, atomic writes,
  refuse-overwrite-without-Force, idempotent default. Same uniform
  CLI result shape across all four so JSON consumers see consistent
  output.
- **M7 — `rebuild-index` CLI.** Wraps the existing
  `index.RebuildAll` (which already powered fetch's auto-rebuild on
  an empty index) behind an explicit user command:
  `agent-memory rebuild-index [--root DIR] [--clobber]
  [--no-assign-ids] [--json]`. Two modes:
  - **Default** runs `DELETE FROM` on the three index tables and
    re-walks `memDir`. Fast; keeps the SQLite file in place.
  - **`--clobber`** removes `meta/index.sqlite`, `-wal`, `-shm`,
    `-journal` siblings before reopening fresh. For genuine SQLite
    corruption where `DELETE FROM` itself would fail.
  Holds the cross-process advisory lock for the duration so a
  concurrent `propose_update` cannot race the wipe-then-rebuild
  window. `--assign-ids` (default on) injects missing
  `<!-- @id: ... -->` anchors on files in categories that require
  them. Documented in
  [docs/patterns/rebuild-index.md](docs/patterns/rebuild-index.md).
- **M5 batch 2 — Staging TTL sweeper + rejection audit log.** Closes
  the two staging-lifecycle gaps from v0.1: stale proposals
  accumulating with no cleanup, and rejections leaving no audit
  trail.
  - `agent-memory sweep [--root DIR] [--ttl DURATION] [--dry-run]
    [--json]` walks `.agent-memory/staging/` and removes every
    proposal older than `manifest.staging.ttl_seconds` (or `--ttl`).
    Each removal also writes a `ttl_expired` entry to the audit log.
  - `meta/rejection-log.jsonl` is the new append-only JSONL audit
    log. One entry per discarded proposal (`user_rejected` from
    `agent-memory reject <id>`, `ttl_expired` from sweep) carrying
    `rejected_at`, `reason`, `staging_id`, `intent`, `rationale`,
    `files`, `staged_at`, `age_seconds`.
  - `agent-memory doctor` gains an advisory `info` finding for
    proposals past TTL, nudging the user toward `agent-memory sweep`
    without taking action itself.
  - `RejectStaged` now writes the audit log entry as well as removing
    the directory. Best-effort: a log write failure doesn't undo the
    removal.
  - Sweep is **explicit only** — no background goroutine, no
    auto-sweep on `propose_update`, no surprise removals while the
    user isn't looking.
  - Documented in [docs/patterns/staging-ttl-and-rejection-log.md](docs/patterns/staging-ttl-and-rejection-log.md).
- **M4 — Git auto-stage / auto-commit on apply.** Two new manifest
  knobs and four lines of orchestration: when
  `manifest.git.auto_stage_changes: true`, every applied file is
  `git add`-ed; when `manifest.git.auto_commit: true` is also set, a
  commit is created with a prefix-+-intent-+-rationale message. Opt-in
  — defaults to off so existing v0.1 deployments upgrade without
  behaviour change.
  - `internal/git/commit.go` exposes `AddPaths(root, paths)` and
    `Commit(root, message)` with safe no-op semantics for non-git
    projects, missing `git` binary, and empty staged index.
  - `internal/memory/autostage.go` adds `shouldStage(file, schema,
    gitCfg)` (category-aware policy that honours `track_local` /
    `track_sessions` overrides) and `maybeAutoStage(...)` (feature-
    gated wrapper that surfaces results without ever failing the
    apply).
  - `ProposeResponse.AutoStage` and `ApplyResult.AutoStage` carry the
    outcome through to CLI + MCP consumers.
  - Auto-stage NEVER runs `git push`, `--no-verify`, `git reset`,
    `git checkout --`, or `git add .`. The file list is always
    explicit.
  - Documented in [docs/patterns/git-auto-stage.md](docs/patterns/git-auto-stage.md).

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

- **M4 — Git integration**: ~~auto-stage / auto-commit on apply (via
  `manifest.git.track_local` / `track_sessions` / `auto_stage_changes`)
  is not yet implemented.~~ Landed in
  [Unreleased](#unreleased--release-02-in-progress); opt-in via
  manifest flags.
- **M5 batch 2 — Staging TTL sweeper**: ~~`manifest.staging.ttl_seconds`
  is parsed but not enforced.~~ Landed in
  [Unreleased](#unreleased--release-02-in-progress) — explicit
  `agent-memory sweep` CLI.
- **M5 batch 2 — Rejection audit log**: ~~discarded proposals leave no
  trace beyond the directory being gone.~~ Landed in
  [Unreleased](#unreleased--release-02-in-progress) —
  `meta/rejection-log.jsonl`.
- **M7 — `rebase` / `rebuild-index` commands**: ~~index repair must
  be done by deleting `meta/index.sqlite*` and re-running fetch.~~
  Both landed in
  [Unreleased](#unreleased--release-02-in-progress). `rebuild-index`
  rebuilds the FTS shadow; `rebase` re-plans staged proposals
  against a new base after external edits.
- **M7 — Git merge driver**: documented in manifest but `init
  --with-merge-driver` is currently a no-op.
- **M8 — Benchmark / eval harness**: ~~`internal/e2e/` has a latency
  regression guard but no formal bench scaffold.~~ Landed in
  [Unreleased](#unreleased--release-02-in-progress).
- **Multi-runtime adapters**: ~~only Claude Code ships in 0.1. Cursor,
  Codex, Gemini, etc. land in 0.2.~~ Three new adapters (cursor,
  agents, gemini) landed in
  [Unreleased](#unreleased--release-02-in-progress).
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

[0.2.0]: https://github.com/xChuCx/agent-memory/releases/tag/v0.2.0
[0.1.0]: https://github.com/xChuCx/agent-memory/releases/tag/v0.1.0
