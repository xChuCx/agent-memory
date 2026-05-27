# Pattern: Staging TTL Sweeper + Rejection Audit Log (M5 batch 2)

**Status:** Implemented in [`internal/memory/sweep.go`](../../internal/memory/sweep.go), [`internal/memory/rejection_log.go`](../../internal/memory/rejection_log.go), CLI in [`internal/cli/sweep.go`](../../internal/cli/sweep.go).
**Owner:** `internal/memory/` + `internal/cli/` (M5 batch 2, Release 0.2).
**Tracks design:** [Design Doc v0.4.1 §24](../../agent-memory-design-doc-v0.4.1.md) (staging engine).

## Problem

Two gaps remained in Release 0.1's staging lifecycle:

1. **Staged proposals accumulate.** An agent that proposes ten
   architectural changes a day, of which the user applies two and
   never reviews the other eight, ends up with a `staging/` directory
   that bloats indefinitely. The manifest already declared
   `staging.ttl_seconds: 604800` (7 days) but nothing enforced it.
2. **Rejections were invisible.** `agent-memory reject <id>` removed
   the directory and left no trace. Auditing "what proposals did I
   throw away last week" required guesswork.

M5 batch 2 closes both gaps with the same primitive: a shared helper
that removes a staging directory **and** appends a structured row to
`meta/rejection-log.jsonl`.

## Design

### The audit log

Plain JSON Lines at `.agent-memory/meta/rejection-log.jsonl`. One
object per line, append-only, no rotation.

```json
{
  "rejected_at": "2026-05-27T15:30:00Z",
  "reason": "user_rejected",
  "staging_id": "20260527T140000-record-decision-use-postgres",
  "intent": "record_decision",
  "rationale": "use postgres for transactional storage",
  "files": ["decisions.md"],
  "staged_at": "2026-05-27T14:00:00Z",
  "age_seconds": 5400
}
```

`reason` is one of:

- `user_rejected` — `agent-memory reject <id>` invocation.
- `ttl_expired` — `agent-memory sweep` removed it because age > TTL.

Future reasons (`superseded`, `policy_violation_at_sweep`, …) extend
the same vocabulary without breaking parsers.

JSONL was picked over a single JSON file or a SQLite table because:

- `tail -f` / `grep` / `jq` work out of the box.
- Append is atomic per line under POSIX `O_APPEND` semantics — no
  rewrite races for concurrent writers.
- No schema migration needed when fields are added (consumers ignore
  unknown JSON keys).

The in-process `rejectionLogMu` serialises writes within a single
process; cross-process safety is provided by the same `meta/lock`
advisory lock that everything else uses. Callers that need
cross-process atomicity acquire that lock first.

### The sweeper

`SweepStale(memDir, ttl, dryRun) → SweepResult` walks `staging/`,
parses each `proposal.json`'s `staged_at`, and removes every entry
whose age exceeds `ttl`. Each removal goes through the shared
`rejectStagedWithReason` helper, so the audit log gets an entry per
removed proposal.

```
SweepResult {
  DryRun  bool
  Expired []ExpiredProposal  // every entry past TTL
  Removed []string           // staging_ids actually deleted
}
```

`dryRun: true` populates `Expired` but skips removal and skips the
audit log write — useful for previews.

`ttl <= 0` short-circuits with an empty result. `manifest.staging.
ttl_seconds: 0` therefore disables the sweeper entirely (it never
removes anything).

### When the sweeper runs

The sweeper is **explicit, never automatic**. Three invocation points:

1. `agent-memory sweep [--root DIR] [--ttl DURATION] [--dry-run] [--json]`
   — the canonical user-facing command. Reads `staging.ttl_seconds`
   from the manifest unless `--ttl` is set.
2. `agent-memory doctor` — when stale proposals exist, emits an
   `info`-severity finding nudging the user toward `sweep`. Doctor
   does NOT remove anything itself — advisory only.
3. The Go API (`memory.SweepStale`) for programmatic consumers.

`agent-memory mcp` deliberately does NOT sweep on every tool call. A
background goroutine would complicate the lock model and surprise
users whose proposals vanish "while they're not looking". Explicit
invocation keeps the model predictable: only `sweep` (or `reject`)
removes staging directories.

## What's intentionally NOT done

- **No background goroutine** — see above. The MCP server is stateless
  request-by-request; there's no daemon that ticks every hour.
- **No automatic invocation during propose_update** — even though the
  orchestrator already holds the advisory lock, an in-line sweep would
  make every write quadratically slower as the staging dir grew, and
  worse, would silently remove proposals the user hadn't yet noticed.
- **No log rotation / retention policy** — `rejection-log.jsonl` is
  append-only forever. Real projects can use logrotate or commit the
  log to git (it's tracked by default since `meta/` is `GitTracked:
  true`).
- **No PII redaction** — the log copies `rationale` and `files`
  verbatim from the staged proposal. Don't put secrets in rationale
  text. Secrets in file content are already scanner-rejected at
  propose-time; the log will never see them.
- **No "undo"** — once `sweep` or `reject` removes a staging dir,
  there's no recovery. The proposal's bytes never landed on the
  durable side of memory; the audit log records intent but doesn't
  archive content. If you need to recover, re-stage from your
  conversation history.

## RejectStaged contract

Before M5 batch 2, `RejectStaged` only removed the directory. Now it
also writes the audit log entry. The wire shape of `ApplyResult` is
unchanged — the audit-log write is a side effect, surfaced only via
`ListRejections(memDir)` for retrospective inspection.

Best-effort discipline: a log write failure does NOT undo the
directory removal. The user's express intent ("get rid of this
proposal") wins over recordkeeping. Mirror of the git auto-stage
contract from M4.

## CLI ergonomics

```
$ agent-memory sweep --dry-run
Would remove 3 staged proposal(s):

  20260520T120000-record-decision-x
    intent:    record_decision
    rationale: ...
    staged:    2026-05-20T12:00:00Z
    age:       7d 3h

  ...

(dry-run; nothing was removed. Re-run without --dry-run to apply.)

$ agent-memory sweep
Removed 3 staged proposal(s):
  ...
```

`--json` emits the full `SweepResult` for scripting:

```bash
agent-memory sweep --json | jq '.removed | length'
```

`--ttl 1h` (or any Go `time.Duration`) overrides the manifest for a
one-off — handy before a release when you want a clean slate without
permanently lowering the manifest TTL.

## Forensics use cases

The audit log is a forensic tool. Some queries it enables:

```bash
# "What did I reject last week?"
jq 'select(.rejected_at > "2026-05-20T00:00:00Z")' meta/rejection-log.jsonl

# "How many TTL-expired vs user-rejected, all time?"
jq -s 'group_by(.reason) | map({reason: .[0].reason, count: length})' \
  meta/rejection-log.jsonl

# "What's the average lifespan of a rejected proposal?"
jq -s 'map(.age_seconds) | add/length' meta/rejection-log.jsonl
```

The log is intentionally readable enough that these one-liners work
without custom tooling.

## References

- [Design Doc v0.4.1 §24 (staging engine + TTL)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.5 M5 batch 2](../../agent-memory-implementation-plan.md).
- [Pattern: Staging Lifecycle](staging-lifecycle.md) — review/apply/reject that this complements.
- [Pattern: Cross-Process Locking](cross-process-locking.md) — the lock cross-process writers should hold before sweep.
