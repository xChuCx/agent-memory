# Agent Memory — MVP Implementation Plan

**Tracks design doc:** `agent-memory-design-doc-v0.4.1.md`
**Plan version:** 0.2 (restructured around risk-based release cuts)
**Status:** Ready for execution after Spikes pass

This plan turns v0.4.1 into a concrete build order. Milestones map to the design doc's M1–M8, with three pre-work spikes added because they de-risk the whole project. Each task has acceptance criteria; each milestone has an exit gate.

---

## 1. Overview

### 1.1 Goal

Ship a single Go binary `agent-memory` that:

- Exposes three MCP tools over stdio.
- Maintains a byte-preserving, branch-aware, concurrency-safe `.agent-memory/` Markdown memory layer.
- Includes worked adapters for Claude Code, Codex, Cursor, and generic agents.
- Passes the evaluation harness on a 3-repo benchmark corpus.

### 1.2 In Scope (MVP, across Releases 0.1–0.3)

All sections marked "Must Have" in design doc §30.1, split across three releases per §1.6: init, fetch, status, review/apply/reject/rebase, rebuild-index, doctor, mcp stdio server, install adapters; byte-preserving engine; per-branch local state; OS-level locking; per-section drift detection; secret scan + allowlist; FTS5 incremental index; archival ops; staging with TTL.

### 1.3 Out of Scope (Deferred)

Vector search; remote sync; web UI; multi-user perms; cloud backend; knowledge graph; auto-compaction; IDE extension; multi-repo workspace; distributed coordination; interactive TUI; PR-comment integration. Listed in design doc §30.2 and §35.

### 1.4 Build Order Rationale

Foundation primitives come first because higher layers depend on them being correct:

```
Spikes → Bootstrap → Foundations (lock + splice + schema) →
Index → Fetch → Updates → Archival → Staging → Adapters → Git → Eval
```

The byte-preserving engine is built before the index because the index pulls section data through the engine. Updates are built before archival because archival is a composite of update operations. Adapters come late because their acceptance is behavioural — the underlying tools must already work.

This is the *dependency* order. The *release* order (§1.6) is different: it cuts the dependency chain at the point where the largest risk — agent behaviour — is testable, even if not every operation is implemented yet.

### 1.5 Rough Timeline (by Release)

For one full-time engineer. Estimates, not commitments; spikes recalibrate.

| Release | Phase | Effort |
|---|---|---|
| 0.1 | Spikes S1–S4 | 3–5 days |
| 0.1 | M0 Bootstrap | 1 day |
| 0.1 | M1 Foundations | 5–7 days |
| 0.1 | M2 Fetch & Index | 4–6 days |
| 0.1 | M3 Structured Updates | 6–8 days |
| 0.1 | M5 subset (review / apply / reject) | 2–3 days |
| 0.1 | M6 subset (Claude adapter only) | 2–3 days |
| 0.1 | **Release 0.1 cut** | **~23–33 days (~5–7 weeks)** |
| 0.2 | M4 Archival (archive / remove / rename) | 2–3 days |
| 0.2 | M5 rest (rebase) | 1–2 days |
| 0.2 | M6 rest (Codex / Cursor / generic) | 2–3 days |
| 0.2 | Secret allowlist polish | 1–2 days |
| 0.2 | **Release 0.2 cut** | **~6–10 days (~1.5–2 weeks)** |
| 0.3 | M7 Git Integration (merge driver, --commit) | 3–4 days |
| 0.3 | M8 Evaluation (corpus, runner, thresholds) | 4–6 days |
| 0.3 | Utility CLIs (clean-local, clean-staging) | 1 day |
| 0.3 | **Release 0.3 cut** | **~8–11 days (~2 weeks)** |
| — | **Total through 0.3** | **~37–54 days (~7.5–11 weeks)** |

Release cuts are explained in §1.6. Each release ships a usable product.

### 1.6 Release Cuts

Milestones (§4–§12) are organized by *dependencies*. Releases are organized by *risk*.

The single largest risk is not engineering — it's whether agents will reliably use the contract (`fetch_context` and `propose_update`) at all. Release 0.1 is sized to answer that question with the smallest possible scope. Engineering breadth — more operations, more adapters, merge driver, eval automation — is comparatively safe; the LLM-behaviour question is not. Isolating it first means a ~5–7 week timeline to a go/no-go decision, instead of ~9–12 weeks to a finished MVP that may need redesign.

#### Release 0.1 — Core Contract Validation

**Goal:** Prove an agent (Claude Code) reliably calls `fetch_context` and `propose_update` through the contract, and that the byte-preserving + concurrency-safe + section-ID core works end-to-end.

**In scope:**

- Spikes S1–S4 (gating).
- M0 Bootstrap.
- M1 Foundations (full).
- M2 Fetch and Index (full).
- M3 Structured Updates (full — five operations, schema validation, secret scanner with basic allowlist).
- M5 subset: `review`, `apply`, `reject`. No `rebase`. No `clean-staging`.
- M6 subset: Claude Code adapter only, with worked SKILL.md.

**Deferred:**

- `archive_section`, `remove_section`, `rename_heading` → 0.2.
- `rebase` for staged updates → 0.2.
- Codex / Cursor / generic adapters → 0.2.
- `apply --commit` flag → 0.3 (raw files left for manual `git commit` is fine for early adopters).
- Merge driver → 0.3.
- Eval runner → 0.3.
- `clean-local`, `clean-staging` → 0.3 (manual cleanup acceptable in 0.1/0.2).

**Validation:** Manual integration test against Claude Code. Agent must call `fetch_context` at session start, call `propose_update` after meaningful change, never generate unified diffs to memory, never bypass tools by direct file edits. Iterate SKILL.md until consistent. If thresholds (design doc §29.5) are reached, the bet is proven. If not, the design needs rework before 0.2 work begins.

#### Release 0.2 — Operation Completeness and Cross-Agent Coverage

**Goal:** Round out the operation vocabulary and prove the design generalizes beyond Claude Code.

**In scope:**

- M4 (full): `archive_section`, `remove_section`, `rename_heading`. Archive ranking penalty in queries. `rebuild-index` CLI exposed.
- M5 completion: `rebase` for drifted staged updates.
- M6 completion: Codex, Cursor, generic adapters with worked SKILL.md / AGENTS.md.
- Secret allowlist polish: better error messages, allowlist-region count surfaced in `status`, documented patterns in adapter SKILL.md.

**Validation:** Each new adapter passes the same manual test bar as Claude. Archive/remove operations work end-to-end via staging.

#### Release 0.3 — Team Workflow and Measurement

**Goal:** Make the product usable for multi-developer teams and quantitatively measurable.

**In scope:**

- M7 (full): merge driver, `apply --commit`, optional pre-commit hook.
- M8 (full): benchmark corpus (start with small/), eval runner, adapter threshold checker.
- Utility CLIs: `clean-local` (orphan branch-local files), `clean-staging` (expired stages).

**Validation:** Two-developer scenario where divergent branches merge cleanly via the driver. Eval runs end-to-end on the small corpus against Claude and produces a reproducible report.

#### Why This Ordering

If `archive_section` is buggy in 0.1, the team can ship 0.1 without it and add it in 0.2. The product is still useful — sections can be replaced; archival is convenience, not a blocker.

If the merge driver doesn't work, two developers can still use the product — they just resolve conflicts manually for now.

But if agents systematically refuse to call the tools or invent their own workflows, the entire premise needs rework. That's the discovery 0.1 is built to make, in ~5–7 weeks instead of ~9–12 weeks.

Each release ships a strictly usable product. 0.1 is for early adopters with disciplined memory hygiene. 0.2 broadens adapter coverage. 0.3 enables team workflows.

---

## 2. Tech Stack & Conventions

### 2.1 Language & Tooling

- Go 1.22+ (for `slices`, `cmp`, modern `errors` features).
- `golangci-lint` (`errcheck`, `staticcheck`, `govet`, `gosec`, `revive`).
- `gofmt`, `goimports`.
- `go test -race` mandatory in CI.

### 2.2 Dependencies

| Need | Library | Notes |
|---|---|---|
| CLI | `github.com/spf13/cobra` | Standard. |
| MCP | `github.com/modelcontextprotocol/go-sdk` | Verify in Spike S2. |
| SQLite | `modernc.org/sqlite` | CGo-free; portable. |
| Markdown AST | `github.com/yuin/goldmark` | AST only — never rendered back. |
| YAML | `gopkg.in/yaml.v3` | Manifest, schema. |
| Diff | `github.com/sergi/go-diff` | Preview generation. |
| File locks | `github.com/gofrs/flock` | OS-level advisory. Verify in Spike S3. |
| Path safety | `path/filepath` stdlib | Plus internal validation helpers. |
| UUID/IDs | `crypto/rand` + base32 stdlib | Staging IDs are timestamp+slug; no UUID needed. |
| Testing fixtures | `github.com/stretchr/testify` (assertions only) | Optional; stdlib works. |

No CGo dependencies. `CGO_ENABLED=0` in CI builds.

### 2.3 Module Layout

Per design doc §27.1. Reproduced for reference:

```text
cmd/agent-memory/main.go

internal/
  app/            # wiring
  cli/            # cobra commands
  mcp/            # MCP server + tools
  memory/         # fetch, update, archive, staging
  index/          # SQLite FTS5 incremental
  markdown/       # parse, section_id, splice, validate
  schema/         # manifest + schema loaders, validators
  security/       # secrets, allowlist, poisoning
  lock/           # flock wrapper
  git/            # branch, diff, commit, merge driver
  adapters/       # claude, codex, cursor, generic
  config/         # manifest types
  fs/             # atomic write, path validation

pkg/protocol/     # public MCP types (if any export needed)
```

### 2.4 Error Model

- Use `errors.New` / `fmt.Errorf("...: %w", err)` consistently.
- Define typed errors for cases callers branch on: `ErrLockHeld`, `ErrSectionNotFound`, `ErrTargetDrift`, `ErrSecretDetected`, `ErrSchemaViolation`, `ErrAmbiguousPrefix`.
- MCP tool responses always return structured JSON with `status` field; never raise raw Go errors over the wire.
- CLI exit codes: 0 success, 1 user error (bad input, ambiguity), 2 system error (disk, lock), 3 validation error (schema, secrets).

### 2.5 Logging

- `log/slog` (stdlib, structured).
- Default level WARN for CLI, INFO for `mcp --stdio` (since MCP stderr is observable by host).
- `--verbose` / `-v` raises to DEBUG.
- Never log secret values; redact via `security` package's `RedactString`.

### 2.6 Testing Conventions

- Table-driven tests where possible.
- Golden-file tests for the byte-preserving engine: input `.md` files in `testdata/markdown/<case>/in.md`, expected output in `testdata/markdown/<case>/out.md`, operation in `op.json`.
- Property tests for the lock: spin N goroutines, each acquires + writes a sentinel; assert serialization.
- Cross-platform CI matrix: ubuntu-latest, windows-latest, macos-latest.

### 2.7 File Format Conventions

- All Markdown files written by the server end in a single trailing newline.
- Existing line endings are preserved on write (LF on POSIX, CRLF on Windows files if they had it).
- BOM is preserved if present.
- UTF-8 only; reject other encodings on read with clear error.

---

## 3. Critical Spikes (Before M1)

Spikes are time-boxed investigations. Each has a written outcome that either confirms the design or surfaces a needed change.

### S1 — Byte-Preserving Markdown Engine POC (1–2 days)

**Goal:** Prove that goldmark exposes byte offsets reliably enough to splice without round-tripping.

**Method:** Write a throwaway program that:

1. Parses a fixture `.md` file into a goldmark AST.
2. Walks the AST collecting `(node_kind, byte_start, byte_end)` for every heading and code fence.
3. Locates a target section by heading text.
4. Computes the section's byte range: from heading line start to start of next heading at same/higher level (or EOF).
5. Splices a replacement string in.
6. Diffs the result against the input byte-for-byte outside the splice range.

**Fixtures to test:**

- Plain headings at various levels.
- Headings preceded by HTML comments.
- Sections containing fenced code blocks with `#` lines that must NOT be treated as headings.
- Files with CRLF line endings.
- Files with BOM.
- Files with YAML frontmatter.
- Files with no trailing newline.
- Sections containing nested headings.
- Sections at end of file (no following heading).
- Duplicate-heading-text sections (needs `occurrence`).

**Outcome:** A markdown decision doc:

- Confirmed approach (use goldmark `ast.Walk` + Source byte ranges).
- Known gotchas (e.g., goldmark may not record byte offsets for all node types — verify).
- API sketch for `internal/markdown/splice.go`.
- If goldmark proves insufficient: alternative is parsing headings via regex against the byte source ourselves (`^#{1,6}\s` pattern, code-fence-aware). Document the decision.

**Exit criteria:** All fixtures round-trip with byte-identical unchanged regions. If any fixture fails, document the limitation and decide on workaround before M1.

### S2 — Go MCP SDK Familiarization (1 day)

**Goal:** Confirm the official SDK's surface matches our needs (3 tools, stdio, structured JSON I/O).

**Method:**

1. Check out `github.com/modelcontextprotocol/go-sdk` latest stable.
2. Build a minimal stdio MCP server exposing one dummy tool: `ping(name) -> {"pong": name}`.
3. Test against Claude Code locally: install via `.claude/settings.json` MCP config, verify Claude calls it.
4. Sketch the three real tool definitions (`fetch_context`, `propose_update`, `status`) with their JSON schemas.

**Outcome:**

- Working minimal MCP server in a `spike/mcp/` directory.
- Notes on: tool registration, error response shape, stdio framing, lifecycle.
- Schema sketches for the three real tools.

**Exit criteria:** Claude Code calls the dummy tool successfully and surfaces the response. If the SDK is unstable, fall back to a handwritten JSON-RPC stdio loop and document the decision.

### S3 — gofrs/flock Cross-Platform Verification (0.5 day)

**Goal:** Verify OS-level lock semantics work on the three target platforms.

**Method:** Write a test that:

1. Spawns 10 goroutines (using `os/exec` to spawn actual subprocesses for true OS lock testing — within-process flock has undefined semantics on some platforms).
2. Each child acquires `meta/lock` via `flock.New(...).TryLock`.
3. On acquire, child writes `<pid>:start\n` to a shared sentinel file, sleeps 50ms, writes `<pid>:end\n`, releases.
4. After all done, parent reads sentinel and asserts no two children's `start..end` ranges overlap.
5. Then: SIGKILL one holder, verify next acquirer succeeds immediately.

Run on linux, windows, macos.

**Outcome:**

- Confirmed behaviour on all three platforms, or documented divergence.
- If Windows has a quirk (e.g., needs `WriteLock` vs `Lock` API), document it.

**Exit criteria:** Test passes on all three CI runners. Process-kill scenario releases lock within 1 second.

### S4 — SQLite FTS5 Incremental Update Pattern (1 day)

**Goal:** Confirm we can update individual sections in FTS5 without rebuilding.

**Method:** Write a script that:

1. Creates an FTS5 table with `(file, section_id, content)`.
2. Inserts 100 fake sections.
3. Updates one section: `DELETE FROM t WHERE file=? AND section_id=?; INSERT INTO t VALUES (?, ?, ?);`.
4. Measures: timing, query correctness post-update.

**Outcome:**

- Confirmed incremental pattern.
- Decision: BM25 ranking via `bm25(t)` MATCH operator, or custom ranking on top.
- Initial benchmark numbers (microseconds per update; index size per N sections).

**Exit criteria:** Per-section update completes in <10ms on a 1000-section index. Query results reflect the update.

---

## 4. M0 — Project Bootstrap (1 day)

**Release:** 0.1.

### 4.1 Tasks

1. **`go mod init`** with chosen module path (e.g., `github.com/<owner>/agent-memory`).
2. Create the layout directories (empty `internal/...` per §2.3) with `.gitkeep` files.
3. Add dependencies to `go.mod`: cobra, goldmark, gofrs/flock, modernc.org/sqlite, yaml.v3, go-mcp-sdk (if S2 confirms), go-diff.
4. Create `cmd/agent-memory/main.go` with a cobra root command and `version` subcommand returning `0.4.1-mvp-dev`.
5. Add `.gitignore` (Go artifacts, IDE, `.agent-memory/meta/index.sqlite*` for dogfood).
6. Add `.github/workflows/ci.yml`: build matrix (ubuntu, windows, macos), `go vet`, `golangci-lint`, `go test -race ./...`.
7. Add `Makefile` (or `Taskfile.yml`) with `build`, `test`, `lint`, `release` targets.
8. Add `README.md` skeleton (description, install, build-from-source, link to design doc).
9. Configure `goreleaser.yaml` (or equivalent) for cross-platform binary builds. Optional: defer to M8.
10. First commit: `bootstrap: project layout, CI, basic cobra root`.

### 4.2 Acceptance

- `go build ./...` succeeds on all three platforms.
- `go test ./...` runs (no tests yet, but the runner works).
- CI green on a no-op PR.
- `agent-memory version` prints `0.4.1-mvp-dev`.

### 4.3 Dependencies

- Spikes S1–S4 outcomes available (any design adjustments folded in).

---

## 5. M1 — Foundations (5–7 days)

**Release:** 0.1. The hard primitives that everything depends on.

### 5.1 Goals

- `agent-memory init` creates a valid `.agent-memory/` layout.
- `agent-memory status` reports basic state.
- Concurrency lock works end-to-end with multi-process tests.
- Byte-preserving Markdown engine handles all spike fixtures.
- `agent-memory doctor` runs basic checks.

### 5.2 Tasks

#### T1.1 — `internal/fs/atomic.go` (S)

API:

```go
func WriteAtomic(path string, data []byte, perm os.FileMode) error
```

Implementation: write to `<path>.tmp.<rand>`, fsync, rename. On Windows, use `os.Rename` which is atomic on NTFS. Tests cover: success, mid-write crash simulation (assert temp file cleanup or detect-and-clean).

#### T1.2 — `internal/fs/paths.go` (S)

Path validation helpers. Refuse `..`, refuse absolute paths, refuse paths outside `.agent-memory/`, refuse paths to derived files (`meta/index.sqlite*`, `meta/lock`).

```go
func ValidateMemoryPath(root, rel string) (clean string, err error)
```

Tests: traversal attempts, symlink attempts (resolve symlinks via `filepath.EvalSymlinks` and re-check).

#### T1.3 — `internal/lock/lock.go` (M)

Wrap `gofrs/flock`. Per design doc §11.3:

```go
type Lock struct { ... }

func Acquire(path string, opts AcquireOpts) (*Lock, error)
func (l *Lock) WriteMetadata(meta Metadata) error
func (l *Lock) ReadMetadata() (Metadata, error)
func (l *Lock) Release() error
```

`AcquireOpts.WaitTimeout` (default 10s, blocking after initial `TryLock`).

Metadata: pid, owner_id, owner_kind, acquired_at, op_id.

Tests: in-process serialization, cross-process serialization (subprocess fixtures), SIGKILL recovery, metadata round-trip.

#### T1.4 — `internal/markdown/parse.go` (M)

Wraps goldmark. Returns:

```go
type Section struct {
  HeadingText  string
  HeadingLevel int
  AnchorID     string    // empty if no @id anchor
  ByteStart    int       // start of heading line
  ByteEnd      int       // start of next heading at same/higher level, or len(src)
  ContentHash  string    // sha256 of bytes [ByteStart, ByteEnd)
}

func ParseSections(src []byte) ([]Section, error)
```

Handles: code fences (skip headings inside them), nested headings, EOF sections, CRLF.

Tests: every fixture from S1.

#### T1.5 — `internal/markdown/section_id.go` (M)

Auto-assign `@id` anchors on first scan:

```go
func AssignMissingIDs(src []byte, sections []Section) (newSrc []byte, idAssignments map[int]string, err error)
```

Returns new bytes with anchors inserted, plus a map of section-index → assigned ID. Idempotent: re-running on output is a no-op.

Slug rule: lowercase, alphanumeric + dash, collapse whitespace, truncate to 64 chars, suffix `-2`, `-3` on collisions within file.

Tests: clean assignment, idempotence, collisions, files with mixed anchored/unanchored sections.

#### T1.6 — `internal/markdown/splice.go` (M)

The core engine.

```go
type SpliceOp struct {
  ByteStart    int
  ByteEnd      int       // exclusive
  Replacement  []byte
}

func Splice(src []byte, ops []SpliceOp) ([]byte, error)
```

Validates ops are non-overlapping and sorted by ByteStart. Applies right-to-left (so earlier offsets remain valid).

Tests: single op, multiple ops, overlap rejection, edge ops (start/end of file).

#### T1.7 — `internal/markdown/validate.go` (S)

Post-splice validation:

```go
func ValidateMarkdown(src []byte) error
```

Checks: parses cleanly through goldmark; no malformed structures.

#### T1.8 — `internal/config/manifest.go` (S)

Manifest YAML loader. Types per design doc §26.1. Defaults applied if fields missing.

```go
type Manifest struct { ... }
func Load(root string) (*Manifest, error)
func WriteDefault(root string) error
```

#### T1.9 — `internal/schema/schema.go` (S)

Schema YAML loader. Types per design doc §25.1. Categories registry, file-glob matching.

```go
type Schema struct { ... }
func Load(root string) (*Schema, error)
func (s *Schema) CategoryForPath(rel string) (Category, bool)
func WriteDefault(root string) error
```

Implementation note: keep schema validation logic minimal in M1; the rich validation in T3.x can call back into the schema later.

#### T1.10 — `internal/cli/init.go` (M)

`agent-memory init [--with-merge-driver]`:

1. Refuse if `.agent-memory/` already exists (unless `--force`, future).
2. Create directory tree.
3. Write default `manifest.yaml`, `schema.yaml`, `.gitignore`.
4. Initialize empty `index.md`, `conventions.md`, `decisions.md`, `pitfalls.md`.
5. Initialize empty `meta/lock` (zero bytes) and `meta/index.sqlite` (initial schema).
6. If `--with-merge-driver`: write `.gitattributes` entry and configure `.git/config`.
7. Print friendly success message with next steps.

Tests: clean init, init-on-existing-dir rejection, init creates valid YAML.

#### T1.11 — `internal/cli/status.go` (S)

`agent-memory status`. Reads manifest, schema, lock metadata, scans `.agent-memory/` for counts, queries SQLite for index size. Outputs human-readable text by default; `--json` for JSON.

#### T1.12 — `internal/cli/doctor.go` (S)

`agent-memory doctor`. Validates layout, checks for orphaned local files, verifies index file is readable, reports issues. Non-fatal: just reports.

### 5.3 Acceptance Gate

- `agent-memory init` in an empty directory produces a directory tree matching §9 layout, all files validate against schema.
- Two concurrent `agent-memory mcp` instances do not corrupt each other's writes (verified via multi-process lock test).
- Byte-preserving engine passes all S1 fixtures plus additional regression cases.
- `agent-memory status` and `doctor` produce usable output.
- Lint clean; `-race` clean; 80%+ coverage of `internal/markdown` and `internal/lock`.

---

## 6. M2 — Fetch and Index (4–6 days)

**Release:** 0.1.

### 6.1 Goals

- SQLite FTS5 shadow index with per-section rows.
- Incremental update API (no full rebuild on every write).
- `agent-memory fetch` CLI returning a budgeted context pack.
- `memory.fetch_context` MCP tool with same semantics.
- Branch-aware local state resolution.

### 6.2 Tasks

#### T2.1 — `internal/git/branch.go` (S)

```go
func ActiveBranch(root string) (BranchInfo, error)

type BranchInfo struct {
  Name        string       // empty if detached
  ShortSHA    string       // for detached
  IsDetached  bool
  IsGitRepo   bool
}
```

Implementation: shell out to `git rev-parse --abbrev-ref HEAD` and `git rev-parse --short HEAD`. Cache result per process (branch doesn't change mid-process for our purposes; CLI invocations are short-lived).

`SlugBranch(name string) string` produces the path slug per §13.1.

#### T2.2 — `internal/index/sqlite.go` (M)

Open/init SQLite database. Schema per design doc §20.2. Migrations: pragma `user_version` for future schema changes.

```go
type Index struct { ... }

func Open(path string) (*Index, error)
func (i *Index) Close() error
func (i *Index) Init() error
```

#### T2.3 — `internal/index/incremental.go` (M)

```go
func (i *Index) UpsertSections(file string, sections []SectionDoc) error
func (i *Index) DeleteSections(file string, sectionIDs []string) error
func (i *Index) DeleteFile(file string) error
```

Per design doc §20.3: `DELETE` then `INSERT` for changed sections; update `memory_docs` row.

#### T2.4 — `internal/index/rebuild.go` (S)

```go
func (i *Index) RebuildAll(root string) error
```

Walks `.agent-memory/`, parses each Markdown file, calls `UpsertSections` per file. Exposed via `agent-memory rebuild-index` CLI.

#### T2.5 — `internal/index/query.go` (M)

```go
func (i *Index) Search(q Query) ([]Result, error)

type Query struct {
  Text     string
  Scope    []string
  Limit    int
}

type Result struct {
  File         string
  SectionID    string
  Heading      string
  Score        float64
  Snippet      string
}
```

Uses FTS5 `MATCH` with BM25. Initial scoring is raw BM25; ranking refinement is in T2.6.

#### T2.6 — `internal/index/ranking.go` (M)

Applies multiplier signals per design doc §20.4:

```go
func ApplyRankingSignals(results []Result, ctx RankingContext) []Result

type RankingContext struct {
  Scope             []string
  ActiveBranch      string
  ArchivePathPrefix string
  StaleFiles        map[string]bool
}
```

#### T2.7 — `internal/memory/fetch.go` (M)

The context pack assembler. Per design doc §20.5:

```go
func BuildContextPack(req FetchRequest, deps FetchDeps) (*ContextPack, error)
```

Implements: bootstrap (empty query), search-based assembly, deduplication, budget enforcement, provenance metadata.

Critical detail: sections are the unit of inclusion. The assembler reads each candidate section's bytes via `markdown.ParseSections` + offset, NOT via the index (index has snippets only; fetch needs full section bytes).

#### T2.8 — `internal/cli/fetch.go` (S)

CLI wrapper. Outputs the assembled pack to stdout (Markdown) or JSON with `--json`.

#### T2.9 — `internal/mcp/server.go` skeleton (S)

Stdio MCP server bootstrap. Initial implementation only registers `fetch_context`. Other tools come in M3 and as needed.

#### T2.10 — `internal/mcp/tools.go` — fetch_context (M)

JSON schema per design doc §15.1. Calls `memory.BuildContextPack`. Returns structured JSON output.

### 6.3 Acceptance Gate

- Empty query returns bootstrap (current.<branch>.md + current.shared.md + conventions.md + index summary), within bootstrap budget.
- Non-empty query returns ranked sections; archive penalized; stale files penalized.
- Branch switch (`git checkout`) followed by `fetch` returns new branch's local state, no caching artifacts.
- Index updates incrementally on simulated file edits (verified by direct API tests).
- `memory.fetch_context` MCP tool callable via mock MCP client; returns correct JSON.
- Fetch performance: <100ms for a 50-section memory on a typical laptop.

---

## 7. M3 — Structured Updates (6–8 days)

**Release:** 0.1. The largest milestone. Implements `memory.propose_update` and all operations.

### 7.1 Goals

- `memory.propose_update` MCP tool.
- Five MVP operations: `create_file`, `append_section`, `replace_section`, `append_to_section`, `replace_section_content`.
- Schema validation per category.
- Secret scanner with allowlist.
- Per-category approval routing (apply vs. stage decision).

### 7.2 Tasks

#### T3.1 — `internal/memory/operation.go` (M)

Operation types and interfaces:

```go
type Operation interface {
  Validate(ctx OpContext) error
  Targets() []OperationTarget         // for drift detection
  Apply(src []byte, ctx OpContext) (newSrc []byte, err error)
}

type OperationTarget struct {
  Path        string
  SectionID   string                  // empty for create_file
  Policy      DriftPolicy
  Hash        string                  // captured at staging time
}
```

Each operation in §15.3–15.10 gets a concrete struct implementing this.

#### T3.2 — Operation implementations (L)

In `internal/memory/ops_*.go`:

- `ops_create_file.go`
- `ops_append_section.go`
- `ops_replace_section.go`
- `ops_append_to_section.go`
- `ops_replace_section_content.go`

Each follows the pattern: validate inputs → resolve target (section or file) → compute splice op → return new src.

Tests: per-op golden tests; edge cases (missing section, duplicate headings without `occurrence`, content header mismatch).

#### T3.3 — `internal/schema/validate.go` (M)

Per-category schema validation. Loaded from `schema.yaml`. Examples of checks:

- `decisions.md` per-section: required fields `Date`, `Status`, `Confidence`, etc., per §25.1.
- `pitfalls.md` bullet format: `Files`, `Confidence`, `Recorded`.
- `conventions.md` required sub-sections.

Implementation: extract structured data from the section bytes (heading + simple regex/parse for required fields), compare against schema.

Tests: synthetic violations per category.

#### T3.4 — `internal/security/secrets.go` (M)

Regex set per §23.2. Entropy detector. Per-line scanning.

```go
type Finding struct {
  Type       string
  Line       int
  Approximate bool
}

func Scan(content []byte, allowlist []AllowlistRegion) ([]Finding, error)
```

Includes `RedactString` for log safety.

Tests: known-good and known-bad token corpus (use fake-looking tokens, not real ones).

#### T3.5 — `internal/security/allowlist.go` (S)

Parses allowlist region markers per §23.6:

```md
<!-- @secret-scan: allow reason="..." -->
...
<!-- @secret-scan: end -->
```

```go
func ExtractAllowlistRegions(content []byte) ([]AllowlistRegion, error)
```

Errors on unmatched open/close, missing reason, nested regions.

#### T3.6 — `internal/security/poisoning.go` (S)

Provenance validation per §23.5:

```go
func ValidateProvenance(intent string, sources []Source, schema CategorySchema) error
```

Enforces: required source types, forbidden source types (e.g., no `external` for decisions), confidence value validity.

#### T3.7 — `internal/memory/update.go` (L)

The update pipeline orchestrator. Implements the sequence from design doc §15.2:

1. Validate input → operations.
2. Acquire lock.
3. Snapshot targets (record current hashes per operation target).
4. For each operation: compute splice op against in-memory source.
5. Run validators (Markdown, schema, secrets, poisoning, budgets, dedup).
6. Decide: apply vs. stage per category routing.
7. If apply: atomic write, incremental index update.
8. If stage: write staging directory.
9. Release lock.
10. Return structured result.

```go
func ProposeUpdate(req ProposeRequest, deps UpdateDeps) (*ProposeResult, error)
```

Tests: integration tests across the full pipeline with mocked deps.

#### T3.8 — `internal/memory/routing.go` (S)

Decides apply-vs-stage per intent and per-target file category. Maps to manifest policy from `updates.approval`.

```go
func DecideRouting(intent string, ops []Operation, manifest *Manifest) Routing

type Routing struct {
  Mode     Mode    // Apply | Stage
  Reason   string
}
```

#### T3.9 — `internal/mcp/tools.go` — propose_update (M)

JSON schema per design doc §15.2. Calls `memory.ProposeUpdate`. Returns Applied / Staged / Rejected response shape per §15.2 outputs.

#### T3.10 — Routing for `session_log` intent (S)

When `intent: "session_log"`, the server routes operations targeting `sessions/<auto-date>.md`. Auto-applies. Allows the agent to record a session without a separate tool (per v0.4 §14).

### 7.3 Acceptance Gate

- Agent can edit a section via section_id without diffing — verified via test fixture.
- Schema violations rejected with structured error per category.
- Secrets rejected with type and location; allowlist regions bypass cleanly.
- Per-category routing correct: decisions→stage, conventions→stage, pitfalls(append)→apply, current→apply, sessions→apply.
- `memory.propose_update` MCP tool callable end-to-end.
- Performance: a single-op apply completes in <50ms (excluding index update); index update incremental and bounded.
- Tests: ≥80% coverage on `internal/memory` and `internal/security`.

---

## 8. M4 — Archival and Removal (2–3 days)

**Release:** 0.2.

### 8.1 Goals

- `archive_section`, `remove_section`, `rename_heading` operations.
- Archive ranking penalty in queries.
- `rebuild-index` CLI.

### 8.2 Tasks

#### T4.1 — `internal/memory/ops_archive.go` (M)

Per design doc §15.8. Multi-step operation:

1. Copy target section bytes to `archive_path` (verify archive file doesn't exist).
2. Splice the replacement string into the source file.
3. Both writes in one staging proposal (or one applied transaction).

Verifies archive path is within `archive/`.

#### T4.2 — `internal/memory/ops_remove.go` (M)

Per design doc §15.9. Archive first, then splice the section out entirely (heading included).

#### T4.3 — `internal/memory/ops_rename_heading.go` (S)

Per design doc §15.10. Splice replaces only the heading line; section bytes after the heading are untouched. ID anchor preserved.

#### T4.4 — Archive ranking integration (S)

Update `index/ranking.go` to apply ×0.4 multiplier to archive files (`archive/*`).

#### T4.5 — `internal/cli/rebuild_index.go` (S)

Wraps `Index.RebuildAll`. Reports timing and entry count.

#### T4.6 — ID assignment migration (S)

`rebuild-index` includes the "assign-missing-ids" pass per design doc §12.5. Walks all files, calls `AssignMissingIDs`, writes back any changes.

### 8.3 Acceptance Gate

- Archive flow: section content preserved in archive file, source file shows replacement string.
- Archive file is write-once; second archive to same path fails.
- Remove: section archived, source no longer contains the section.
- Rename heading: ID preserved, byte-stable outside heading line.
- `rebuild-index` is idempotent: running twice produces identical state.

---

## 9. M5 — Staging and Review (4–5 days)

**Release:** split — 0.1 ships `review` / `apply` / `reject`; 0.2 adds `rebase`; 0.3 adds `clean-staging`.

### 9.1 Goals

- Staging directory layout per §16.3.
- TTL handling.
- Per-section drift detection per §16.4.
- `review`, `apply`, `reject`, `rebase`, `clean-staging` CLI.
- Prefix matching for staging IDs.

### 9.2 Tasks

#### T5.1 — `internal/memory/staging.go` (M)

```go
func WriteStaging(proposal Proposal, root string) (stagingID string, err error)
func ReadStaging(id string, root string) (*Proposal, error)
func ListStaging(root string) ([]StagingEntry, error)
func ResolvePrefix(prefix string, root string) (string, error)
```

Staging ID format: `<rfc3339-utc-compact>-<slug>`, e.g., `2026-05-26T121500-auth-token-rotation`. Slug is derived from intent and rationale (first 32 chars, slugified).

Prefix resolution: lists staging IDs, filters by `strings.Contains` (prefix or substring of the full ID). On multiple matches, return `ErrAmbiguousPrefix` with candidates.

#### T5.2 — `internal/memory/drift.go` (M)

Per-operation drift policy enforcement per §16.4 table:

```go
func CheckDrift(proposal Proposal, currentRoot string) (*DriftReport, error)
```

For each operation target:

- `require_section_content_match`: re-parse target file, find section by ID, hash content, compare.
- `require_section_resolvable`: re-parse target file, find section by ID. Don't compare hash.
- `require_file_absent`: stat file, expect not-found.
- `require_file_present`: stat file, optionally verify parent section resolves for `append_section`.

Returns `DriftReport` listing per-operation drift status.

#### T5.3 — `internal/cli/review.go` (M)

`agent-memory review [<id_prefix> | --latest] [--since <duration>]`. Per design doc §16.7 output: rationale, sources, operations summary, preview diff, schema status, security status, drift status, age, TTL.

Implements prefix resolution via T5.1.

#### T5.4 — `internal/cli/apply.go` (M)

`agent-memory apply (<id_prefix> | --latest) [--commit]`:

1. Resolve staging ID.
2. Acquire lock.
3. Run drift check (T5.2). On drift, return error with details.
4. Replay operations against current state.
5. Run all validators again.
6. Atomic write.
7. Incremental index update.
8. If `--commit`: shell out to `git add` + `git commit` of durable memory files with manifest-configured prefix.
9. Delete staging directory.
10. Release lock.

#### T5.5 — `internal/cli/reject.go` (S)

`agent-memory reject (<id_prefix> | --latest)`. Resolves ID, deletes staging directory.

#### T5.6 — `internal/cli/rebase.go` (M)

`agent-memory rebase (<id_prefix> | --latest)`. Re-resolves each operation's section_id against current file state. Refreshes `expected_section_hash`. Writes new staging proposal (same ID, replacing the old contents). Fails if any target section was deleted entirely.

#### T5.7 — `internal/cli/clean_staging.go` (S)

`agent-memory clean-staging`. Iterates staging directory, removes entries past TTL.

#### T5.8 — Update `memory.status` MCP tool to surface staging (S)

Add staged updates summary to the `status` MCP tool output per design doc §15.11.

### 9.3 Acceptance Gate

- Stage workflow: agent calls `propose_update` on a `decisions/` target → server returns `status: staged`.
- Human runs `review --latest` → sees structured diff and metadata.
- `apply` succeeds when no drift; fails cleanly with `target_drift` when target section content changed.
- `rebase` updates section hashes; subsequent `apply` succeeds.
- Prefix matching works: `apply 2026` resolves uniquely or lists candidates.
- TTL expiry: `clean-staging` removes entries past `staging.ttl_seconds`.
- Drift detection is per-section: an unrelated edit to another section in the same file does NOT trigger drift.

---

## 10. M6 — Adapters (3–5 days)

**Release:** split — 0.1 ships Claude Code adapter only; 0.2 adds Codex, Cursor, generic. The agent-quality work. Heavy iteration; success measured behaviourally.

### 10.1 Goals

- Worked SKILL.md (`adapters/claude.go` generates it per §18.5).
- All four adapters install correctly.
- Idempotent block markers in `CLAUDE.md` / `AGENTS.md` / Cursor rules.
- Adapter acceptance threshold check (deferred concrete eval to M8).

### 10.2 Tasks

#### T6.1 — `internal/adapters/common.go` (S)

Shared utilities: file path resolution, idempotent block marker insertion/update, dry-run mode.

```go
type Adapter interface {
  Install(target string, mode Mode) (*InstallReport, error)
  Files() []string
}

func InsertOrUpdateBlock(content []byte, marker, payload string) ([]byte, bool, error)
```

Marker pattern: `<!-- agent-memory:begin --> ... <!-- agent-memory:end -->`.

#### T6.2 — `internal/adapters/claude.go` (M)

Generates:

- `CLAUDE.md` block referencing `.claude/skills/project-memory/SKILL.md`.
- `.claude/skills/project-memory/SKILL.md` — the worked example from design doc §18.5.
- `.claude/settings.json` MCP server entry.

If files exist, only update marked block. If `settings.json` exists, merge JSON (preserving user keys).

#### T6.3 — `internal/adapters/codex.go` (M)

Generates:

- `AGENTS.md` block.
- `.agents/skills/project-memory/SKILL.md`.

#### T6.4 — `internal/adapters/cursor.go` (S)

Generates `.cursor/rules/agent-memory.mdc`.

#### T6.5 — `internal/adapters/generic.go` (S)

Generates a generic `AGENTS.md` with CLI fallback instructions.

#### T6.6 — `internal/cli/install.go` (S)

`agent-memory install (claude|codex|cursor|generic) [--dry-run]`. Wraps adapter invocation. Reports files created/updated.

#### T6.7 — Manual integration test against Claude Code (M)

Not automatable in CI. Document the test protocol:

1. Init test repo, install Claude adapter.
2. Open Claude Code in repo.
3. Verify Claude calls `memory.fetch_context` at session start.
4. Verify Claude calls `propose_update` after a meaningful change.
5. Verify Claude does NOT generate unified diffs to memory files.
6. Iterate the SKILL.md prompt until thresholds (§18.1) hit.

### 10.3 Acceptance Gate

- All four adapters install cleanly into fresh and pre-existing repos.
- Idempotent block markers preserve user content outside the markers.
- Re-install only updates the marked block.
- `--dry-run` shows what would change without writing.
- Manual integration test against Claude Code passes acceptance thresholds (§18.1, §29.5). Codex and Cursor are best-effort; full validation deferred to M8.

---

## 11. M7 — Git Integration (3–4 days)

**Release:** 0.3.

### 11.1 Goals

- `init --with-merge-driver` configures custom merge driver.
- `agent-memory merge-driver` command operates as Git merge tool.
- `apply --commit` shells out to git.
- Optional pre-commit hook installer.

### 11.2 Tasks

#### T7.1 — `internal/git/commit.go` (S)

```go
func StageMemoryFiles(root string) error
func Commit(root, prefix, summary string) error
```

Shells out to system `git`. Refuses if working tree has unrelated staged changes (warn and skip; only commit `.agent-memory/` paths).

#### T7.2 — `internal/git/diff.go` (S)

```go
func DiffNoIndex(a, b []byte) ([]byte, error)
```

Shells out to `git diff --no-index --` between temp files; returns the diff bytes. Used for staging `preview.diff`.

#### T7.3 — `internal/git/merge_driver.go` (L)

The actual merge driver per design doc §24.5:

```go
func MergeDriver(base, ours, theirs, path string) error
```

For each section by ID:

- Both unchanged: skip.
- One side changed: take that side.
- Both changed: write a `<!-- @merge-conflict @id=X -->` block containing both versions.
- Added on one side only: include.
- Removed on one side: keep surviving version, warn.

Writes result to `ours` path.

#### T7.4 — `internal/cli/merge_driver.go` (S)

`agent-memory merge-driver %O %A %B %P` — wraps T7.3.

#### T7.5 — Update `init.go` for `--with-merge-driver` (S)

Writes `.gitattributes`:

```text
.agent-memory/**/*.md merge=agent-memory
```

Configures `.git/config`:

```text
[merge "agent-memory"]
  name = Agent Memory section-aware merge
  driver = agent-memory merge-driver %O %A %B %P
```

#### T7.6 — Optional pre-commit hook (S)

`agent-memory install hook` (or part of `init`): writes a `.git/hooks/pre-commit` that runs `agent-memory doctor --strict`. Opt-in.

### 11.3 Acceptance Gate

- Two divergent branches editing non-overlapping sections of the same file merge cleanly via the driver.
- Two divergent branches editing the same section produce a `@merge-conflict` block readable by humans.
- `apply --commit` produces a clean `chore(memory):` commit.
- Tests: merge driver tested with synthetic 3-way scenarios.

---

## 12. M8 — Evaluation Harness (4–6 days)

**Release:** 0.3.

### 12.1 Goals

- Benchmark corpus: 3 seeded repos (small, medium, large).
- Eval runner that drives an agent through tasks and records metrics.
- Adapter acceptance threshold check.
- CI integration (or at least documented manual runs).

### 12.2 Tasks

#### T8.1 — Benchmark corpus design (M)

Per design doc §29.1. Create three test repos as fixtures under `testdata/eval/`:

- `small/` — ~50 files, 1 module, 10 tasks.
- `medium/` — ~500 files, 5 modules, 20 tasks. (May start with smaller fixture, document growth path.)
- `large/` — deferred; seed with placeholder for v0.5.

Each task has: description, golden output (expected file changes), expected memory tool calls, expected memory updates.

#### T8.2 — `internal/eval/runner.go` (L)

Drives an agent through a task. Records:

- Tokens consumed (if the agent host exposes it).
- Tool calls made.
- Files read.
- Wall-clock time.
- Task completion: compare final state to golden.

Implementation note: this is the hardest M8 task because it requires an interface to the agent host. For MVP, scope to:

- Claude Code CLI invocation in a subprocess.
- Capture stdout/stderr and tool-call trace from session logs.

If that proves too fragile in v0.4.1 timeframe, alternative: a simpler "behavioural assertion" runner that requires the agent to be invoked manually and checks artifacts left in the repo afterward.

#### T8.3 — `internal/cli/eval.go` (M)

`agent-memory eval [--corpus small] [--agent claude] [--baseline none|agents-md|memory]`. Per design doc §29.3 — three configurations to compare.

#### T8.4 — Adapter threshold checker (S)

```go
func CheckAdapterThresholds(results []EvalResult) (*ThresholdReport, error)
```

Per §29.5: ≥80% fetch_context calls, ≥60% propose_update calls, 100% no unified diffs, ≥95% no direct file edits.

#### T8.5 — Eval results storage (S)

JSON output per run. Optional: append to `.agent-memory/meta/eval-history.jsonl`.

### 12.3 Acceptance Gate

- Eval runs end-to-end on at least the `small/` corpus against Claude Code.
- Metrics reported in a readable format.
- Adapter acceptance thresholds measurable and reported.
- Adapter SKILL.md iteration produces a measurable improvement in tool-call rate (documented in eval-history).

---

## 13. Cross-Cutting Concerns

### 13.1 Error Model

- Sentinel errors in `internal/memory/errors.go`:
  - `ErrLockHeld` — lock not acquirable in timeout.
  - `ErrSectionNotFound` — target section_id not in file.
  - `ErrAmbiguousSection` — multiple sections match heading without occurrence.
  - `ErrTargetDrift` — section content hash mismatch on apply.
  - `ErrSecretDetected` — content flagged by scanner.
  - `ErrSchemaViolation` — content doesn't match category schema.
  - `ErrAmbiguousPrefix` — staging ID prefix matches multiple.
  - `ErrInvalidPath` — path outside `.agent-memory/` or contains `..`.

- All MCP responses use the structured `status` field per design doc §15.2 (Applied/Staged/Rejected).
- CLI errors print to stderr, exit code per §2.4.

### 13.2 Logging

- `log/slog` configured at startup.
- CLI logs to stderr; MCP server logs to stderr (stdout is for JSON-RPC frames).
- Sensitive values redacted via `security.RedactString`. NEVER log a full secret value, even on the `secret_detected` rejection path — log type and approximate location only.

### 13.3 Configuration Loading

- `internal/config.Load` searches:
  1. Explicit `--root` flag.
  2. Walk up from CWD looking for `.agent-memory/`.
  3. Error if none found.
- Manifest defaults applied via reflection-based merge or explicit per-field defaults.

### 13.4 Testing Strategy

- **Unit tests:** per package. Target ≥70% coverage; ≥85% for `markdown/`, `lock/`, `memory/`.
- **Integration tests:** under `internal/integration/` — exercise the whole pipeline (init → propose → apply) against a temp repo.
- **Golden-file tests:** for the Markdown engine. Fixtures in `testdata/markdown/<case>/`. Update via `go test -update`.
- **Multi-process tests:** for lock. Spawn child processes via `os.Exec(os.Args[0], "--test-lock-helper", ...)`.
- **Cross-platform CI:** ubuntu, windows, macos. Catches path-separator and line-ending issues early.
- **Race detection:** `go test -race ./...` in CI on linux.
- **Mocking:** prefer interfaces with test doubles over `gomock`. The dependency surface is small.

### 13.5 Documentation

- `README.md` — install, quick start, link to design doc.
- `docs/` — usage guides, schema reference (extracted from `schema.yaml`).
- Inline godoc for public types and key internal interfaces.

---

## 14. Risk Register

| Risk | Likelihood | Impact | Mitigation | Owner Milestone |
|---|---|---|---|---|
| goldmark byte offsets unreliable | Medium | High | Spike S1 confirms before M1. Fallback: regex-based heading detector with code-fence awareness. | S1 / M1 |
| Go MCP SDK immature / churning | Medium | Medium | Spike S2. Fallback: handwritten JSON-RPC stdio loop. | S2 / M2 |
| FTS5 performance on large memory | Low | Medium | Spike S4 benchmarks. Index size stays small for MVP corpus. | S4 / M2 |
| Adapter quality below thresholds | High | High | M6 includes manual iteration; M8 measures. Budget for ≥2x iteration cycles. | M6 / M8 |
| Markdown roundtrip edge case found late | Medium | High | Aggressive golden-file fixtures from S1 onward; CI catches regressions. | M1+ |
| Lock contention bottleneck | Low | Medium | Single-writer is design constraint; contention only matters if two agents push hard concurrently. Acceptable for MVP. | M1 |
| Per-section drift policy too strict | Medium | Medium | Append uses weaker policy. If false positives appear, tune in M5. | M5 |
| Schema validation overly rigid | Medium | Medium | Start permissive; tighten via project feedback in v0.5. | M3 |
| Branch slugging collisions | Low | Low | `feature/auth` and `feature_auth` could collide. Detect and warn at write time. | M2 |
| Eval harness too coupled to Claude Code | Medium | Low | M8 documents the limitation; v0.5 adds Codex and Cursor runners. | M8 |
| Secret-scan false positives blocking work | Medium | Medium | Allowlist mechanism (M3 §23.6). Document the pattern in the SKILL.md. | M3 |
| Cross-platform path bugs | Medium | Medium | CI matrix from M0 onward. `filepath` everywhere, no string concat. | M0+ |

---

## 15. Definition of Done (Per Release)

Each release has its own shippable bar. Earlier criteria carry forward into later releases.

### 15.1 Release 0.1 — Core Contract Validation

**Functional:**

- `agent-memory init` produces a valid layout per design doc §9.
- `agent-memory fetch` returns a budgeted, ranked context pack.
- `agent-memory status` reports memory health, branch, lock state.
- `memory.fetch_context` MCP tool works against Claude Code.
- `memory.propose_update` MCP tool routes correctly (apply or stage) per category.
- `memory.status` MCP tool reports staged updates.
- Five MVP operations (`create_file`, `append_section`, `replace_section`, `append_to_section`, `replace_section_content`) work and pass golden-file tests.
- `review`, `apply`, `reject` CLI commands work; staging-ID prefix matching works.
- Per-section drift detection blocks bad applies; allows unrelated edits.
- Secret scanner catches the design doc §23.2 token corpus; basic allowlist works.
- Schema validation enforces per-category rules.
- Claude Code adapter installs with worked SKILL.md; idempotent block markers preserve user content.

**Non-functional:**

- Single binary builds for linux, windows, macos (amd64 + arm64).
- No CGo dependencies.
- Fetch latency <100ms on the small corpus.
- Apply latency <50ms (excluding any git operations).
- Index incremental: per-section update <10ms.
- `go test -race` clean.
- Lint clean (golangci-lint).
- Cross-platform CI green.

**Documentation:**

- README with install + quick start + design doc link.
- Inline godoc for public types and key internal interfaces.

**Validation (the bet):**

- Manual integration test against Claude Code meets acceptance thresholds (design doc §18.1, §29.5):
  - `fetch_context` before reading untouched-module code: ≥80% of trials.
  - `propose_update` after meaningful change: ≥60% of trials.
  - Zero unified diffs to memory files.
  - Direct file edits bypassing tools: ≤5%.
- If thresholds are not met after reasonable SKILL.md iteration, **stop and reconsider the design before continuing to 0.2**.

### 15.2 Release 0.2 — Operation Completeness and Cross-Agent

**Functional (in addition to 0.1):**

- `archive_section`, `remove_section`, `rename_heading` operations work; archive is write-once.
- Archive ranking penalty applied in queries.
- `rebuild-index` CLI is exposed and idempotent.
- `rebase` re-resolves staged updates; refreshes per-section hashes.
- Codex, Cursor, generic adapters install with worked SKILL.md / AGENTS.md.
- Secret allowlist polish: region count surfaced in `status`, improved error messages.

**Documentation:**

- Per-adapter README.

**Validation:**

- Each new adapter passes the same threshold bar as Claude in manual testing.

### 15.3 Release 0.3 — Team Workflow and Measurement

**Functional (in addition to 0.2):**

- `init --with-merge-driver` configures and `agent-memory merge-driver` works.
- Two divergent branches editing non-overlapping sections merge cleanly.
- Two divergent branches editing the same section produce a reviewable `@merge-conflict` block.
- `apply --commit` produces a clean `chore(memory):` commit.
- `clean-local` removes orphan branch-local files (dry-run + apply modes).
- `clean-staging` removes expired staging entries.

**Documentation:**

- Migration guide for users coming from naive `AGENTS.md` setups.
- Schema reference (extracted from `schema.yaml`).

**Eval:**

- Benchmark corpus (`small/`) committed.
- Eval runner produces a reproducible report.
- Adapter acceptance thresholds met for at least Claude on `small/`.
- Eval report committed to repo for posterity.

---

## 16. First PR Checklist (Path to Release 0.1)

Concrete first steps toward Release 0.1 — the core contract validation cut.

1. Spike S1 in a throwaway directory; produce a 1-page decision doc.
2. Spike S2 in a throwaway directory; confirm SDK choice.
3. Spike S3 + S4 in throwaway directories; record outcomes.
4. Open the repo for the project (or `git init` here).
5. `go mod init <module-path>`.
6. Create directory tree per §2.3 with `.gitkeep`.
7. Add dependencies to `go.mod`.
8. Create `cmd/agent-memory/main.go` with cobra root + `version` subcommand.
9. Add `.gitignore`, `.editorconfig`.
10. Add `.github/workflows/ci.yml` (build matrix + test + lint).
11. Add `Makefile` with `build`, `test`, `lint`, `release` targets.
12. Add `README.md` skeleton.
13. Open first PR: "bootstrap: project layout, CI, basic cobra root".
14. After merge: start T1.1 (atomic write) — the smallest standalone task in M1.

---

## 17. Post-MVP Backlog (Deferred)

Tracked here so they don't pollute MVP planning. From design doc §35 and §29.6:

### v0.5

- Assisted compaction.
- File-change-aware context suggestions.
- Stale-note detection refinements.
- GitHub Action for validation + PR comments.
- PR-based review workflow.
- Human/agent author tracking in memory.
- Configurable secret rule sets.
- Interactive TUI (`bubbletea`) for `review` selector.
- Codex + Cursor adapter eval at full thresholds.

### v0.6

- Multi-repo workspace memory.
- Per-task overlay state.
- Memory quality scoring.
- Conflict-resolution wizard.

### v1.0

- Stable memory format & MCP contract.
- Production-ready safety defaults.
- Proven team workflow.

### Later

- Optional vector search (only if BM25 proven insufficient).
- Rust indexer (only if profiling justifies).
- Web dashboard / sync backend.
- IDE extension.

---

## 18. Open Decisions Needed Before/During Execution

Issues the plan surfaces that need a yes/no during the build:

1. **Module path.** `github.com/<who>/agent-memory`? Decide before M0.
2. **License.** MIT? Apache 2.0? Both work; decide before public CI / release.
3. **Release distribution.** GoReleaser → GitHub Releases? Homebrew tap? Scoop?
4. **Eval corpus storage.** Inline in repo? Separate fixtures repo?
5. **Manual eval cadence.** Weekly? Per release candidate?
6. **Branch-slug collision handling.** Warn or refuse? (Low likelihood but undefined.)
7. **Schema versioning.** What happens when `schema.yaml` is bumped on an existing repo? Migration in v0.5.
8. **Logging verbosity defaults.** WARN for CLI is opinionated; could be INFO if user feedback suggests.
9. **MCP SDK pinning.** If the SDK is in alpha, pin to a specific commit; document upgrade plan.
10. **`pkg/protocol`.** Is anything actually exported? If not, drop the package.

---

## Appendix A — Critical API Sketches

The handful of interfaces that downstream code depends on. Stable design before coding.

### A.1 Markdown Engine

```go
// internal/markdown/parse.go

type Section struct {
    HeadingText  string
    HeadingLevel int
    AnchorID     string
    Occurrence   int       // for duplicate headings
    ByteStart    int
    ByteEnd      int
    ContentHash  string
}

func ParseSections(src []byte) ([]Section, error)
func AssignMissingIDs(src []byte) (newSrc []byte, assigned []string, err error)
func FindByID(sections []Section, id string) (*Section, bool)
func FindByHeading(sections []Section, heading string, level int, occurrence int) (*Section, bool)

// internal/markdown/splice.go

type SpliceOp struct {
    ByteStart   int
    ByteEnd     int       // exclusive
    Replacement []byte
}

func Splice(src []byte, ops []SpliceOp) ([]byte, error)
```

### A.2 Lock

```go
// internal/lock/lock.go

type AcquireOpts struct {
    WaitTimeout time.Duration   // 0 = TryLock only; >0 = blocking with timeout
    Owner       Metadata
}

type Metadata struct {
    OwnerPID   int
    OwnerID    string
    OwnerKind  string
    AcquiredAt time.Time
    OpID       string
}

type Lock struct { ... }

func Acquire(path string, opts AcquireOpts) (*Lock, error)
func (l *Lock) Release() error
func ReadMetadata(path string) (Metadata, error)
```

### A.3 Operation

```go
// internal/memory/operation.go

type Operation interface {
    Kind() string
    Path() string
    Validate(schema *schema.Schema) error
    Targets() []OperationTarget
    Plan(src []byte) (SpliceOp, error)
}

type OperationTarget struct {
    Path       string
    SectionID  string
    Policy     DriftPolicy
    Hash       string             // populated at staging time
}

type DriftPolicy int
const (
    RequireSectionContentMatch DriftPolicy = iota
    RequireSectionResolvable
    RequireFileAbsent
    RequireFilePresent
)
```

### A.4 MCP Tool I/O

Per design doc §15. Wrapped in Go structs with explicit JSON tags. Generated JSON schemas exposed via the MCP SDK's tool registration.

---

## Appendix B — Test Fixture Examples

Each `testdata/markdown/<case>/` directory contains:

- `in.md` — input file
- `op.json` — operation to apply (one of the v0.4.1 operations, in JSON)
- `out.md` — expected output

Example: `testdata/markdown/replace-section-simple/`

`in.md`:

```md
# Module
<!-- @id: module -->

## Token Rotation
<!-- @id: token-rotation -->

Old content here.

## Other Section
<!-- @id: other-section -->

Untouched.
```

`op.json`:

```json
{
  "operation": "replace_section",
  "path": "test.md",
  "section_id": "token-rotation",
  "content": "## Token Rotation\n<!-- @id: token-rotation -->\n\nNew content here.\n"
}
```

`out.md`:

```md
# Module
<!-- @id: module -->

## Token Rotation
<!-- @id: token-rotation -->

New content here.

## Other Section
<!-- @id: other-section -->

Untouched.
```

The test asserts byte-equality between produced output and `out.md`, AND byte-equality of unchanged regions (everything before and after the spliced section).

Categories of fixtures to seed:

- `replace-section-simple/`
- `replace-section-with-code-fence/`
- `replace-section-crlf/`
- `append-section-end-of-file/`
- `append-section-in-parent/`
- `append-to-section-bullet/`
- `archive-section-full/`
- `remove-section/`
- `rename-heading-preserves-id/`
- `create-file-new/`
- `id-assignment-fresh/`
- `id-assignment-idempotent/`
- `id-assignment-collision/`
- `byte-preservation-untouched-regions/`

---

## Appendix C — Sample CI Configuration

```yaml
# .github/workflows/ci.yml
name: CI

on:
  pull_request:
  push:
    branches: [main]

jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
        go: ['1.22']
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go }}
      - run: go build ./...
      - run: go test ./...
      - if: matrix.os == 'ubuntu-latest'
        run: go test -race ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
```

---

## Closing Notes

This plan is a working document. Adjust as spikes calibrate effort and as risks materialize. Track milestone progress in GitHub Issues / a simple project board; do not let the plan and reality diverge silently. Re-read this doc at the start of each milestone.

Two bets, two checkpoints. The largest **technical** leverage point is the byte-preserving Markdown engine — if S1 surfaces a fundamental limitation, stop and discuss before M1. The largest **product** leverage point is whether agents reliably use the contract — if Release 0.1's manual integration test fails after reasonable SKILL.md iteration, stop and discuss before 0.2. Everything else is engineering on top of these two bets being right.
