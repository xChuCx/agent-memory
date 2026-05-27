# Pattern: propose_update Pipeline (T3.7 orchestrator)

**Status:** Implemented in [`internal/memory/update.go`](../../internal/memory/update.go), [`internal/memory/routing.go`](../../internal/memory/routing.go).
**Owner:** `internal/memory/` (M3 batch 3).
**Tracks design:** [Design Doc v0.4.1 §22](../../agent-memory-design-doc-v0.4.1.md).

## Problem

A single `propose_update` MCP call from an agent must atomically:

1. Parse multiple operations against a typed `Operation` interface.
2. Resolve each path to a schema **category** and confirm the agent is allowed to write it.
3. Apply the operations **sequentially in memory** so later ops see the bytes produced by earlier ones — never disk state.
4. Validate the final per-file Markdown bytes (parse-clean, schema-clean, secret-clean).
5. Decide an approval policy via per-intent routing: apply, stage, or refuse.
6. Either WriteAtomic the result + re-index, OR write a staging directory for later human review.
7. Hold a cross-process advisory lock for the entire window.

Any single failure rejects the **entire** proposal — there is no partial application. Failed proposals return a stable `Reason` code the caller can match against.

## Pipeline

```
ProposeUpdate(req, deps)
├─ 1. validate request shape           — IsValidIntent, len(ops)>0
├─ 2. session_log path rewrite          — rewrite to sessions/<UTC today>.md
├─ 3. parse + per-op Validate(schema)  — invalid_intent | invalid_operation | validation_failed
├─ 4. path + category resolution        — invalid_path | unknown_category | server_managed_category
│       ValidateMemoryPath + Schema.CategoryForPath
├─ 5. lock acquire                       — lock_held (timeout from manifest.Concurrency.WaitTimeoutSeconds)
├─ 6. per affected file:
│     a. read preState (nil if absent)
│     b. apply ops sequentially:
│          src ← preState
│          for each op: splice ← op.Plan(src); src ← Splice(src, [splice])
│        postState ← src
│     c. ValidateMarkdown(postState)    — invalid_markdown
│     d. Schema.ValidateSection(...)    — validation_failed (only when category has SectionSchema)
│     e. ExtractAllowlistRegions        — allowlist_parse_error
│     f. Scan(post, ...)                — secret_detected
├─ 7. ValidateProvenance(...)            — provenance_violation
├─ 8. DecideRouting per op + Combine    — server_only → reject server_only_category
│                                          stage      → branch to stageProposal
│                                          apply      → branch to applyImmediately
└─ release lock (defer)
```

### Sequential planning, not parallel

The orchestrator processes operations on the **same file** in input order, threading the post-op bytes through each subsequent op's `Plan()`. This is the only way multi-op proposals like

```
1. create_file local/current.shared.md (replace)
2. append_section to that file
```

can work — step 2's `Plan()` must see step 1's bytes, not the seed file on disk.

Within a single `Plan` call the orchestrator passes only the **current in-memory bytes**, never re-reads. The byte-preserving Markdown engine guarantees splice-then-splice composes cleanly: each splice modifies one byte range, and the next op operates on the result.

### What gets validated, by which step

| Step | Concern |
|------|---------|
| `op.Validate(schema)` | Per-op structural shape: required fields populated, content parses as Markdown, if_exists / if_missing in allowed enum |
| Path validation | Path stays inside `.agent-memory/`, doesn't target `meta/index.sqlite` or `meta/lock` (derived) |
| Category resolution | A category matches the path; agent isn't writing a `server_managed` category |
| `ValidateMarkdown(post)` | Final bytes round-trip through goldmark — catches splice mistakes that produced non-Markdown |
| `schema.ValidateSection` | Per-section required fields / patterns / enums from the category's `SectionSchema` |
| `Scan` + `ExtractAllowlistRegions` | Credentials in the post-state bytes (with allowlist regions excluded) |
| `ValidateProvenance` | Required source citations for the dominant category's policy |

## Routing decisions

`routing.go` owns the intent → manifest-slot map:

| Intent | Slot in `manifest.updates.approval` | Default |
|--------|-------------------------------------|---------|
| `update_current` | `current` | apply |
| `update_shared` | `current_shared` | apply |
| `session_log` | `sessions` | apply |
| `add_pitfall` + `append_to_section` | `pitfalls_append` | apply |
| `add_pitfall` + anything else | `pitfalls_replace` | stage |
| `record_decision` | `decisions` | stage |
| `refresh_module` | `modules` | stage |
| `update_conventions` | `conventions` | stage |
| `archive_stale` | `archive` | stage |

`CombineRoutings` reduces per-op routings to a single proposal-level decision: **most restrictive wins** (`server_only` > `stage` > `apply`). One server-only op poisons the whole proposal.

### Why `add_pitfall` splits by op kind

Appending a new bullet to an existing pitfall section is low-risk (additive, easily reversible). Rewriting an entire pitfall section can drop hard-won knowledge — the user wants a second pair of eyes on it. The split lets us auto-apply the safe case and stage the risky one without changing the intent vocabulary.

## Staging directory layout

On `stage` routing, `stageProposal` materialises the proposal under `.agent-memory/staging/<id>/`:

```
staging/
└── 20260527T143012-record-decision-use-postgres/
    ├── proposal.json          ← full ProposeRequest + Routing + Files list
    ├── target-checksums.json  ← []OperationTarget with Hash filled in
    └── files/                  ← post-state bytes, mirror of memory layout
        └── decisions.md
```

The M5 `apply <id>` CLI will:

1. Re-read each `OperationTarget` in `target-checksums.json`.
2. For `require_section_content_match`: re-compute the section's hash on the now-current disk state; if it differs from the stored Hash, drift has happened → reject with `target_drift`.
3. For `require_section_resolvable`: confirm the section still resolves by ID.
4. For `require_file_absent` / `require_file_present`: stat the file.
5. If all checks pass: `WriteAtomic` each file from `files/` onto disk.

### Staging ID format

```
<UTC YYYYMMDDTHHMMSS>-<slug(intent + rationale, max 40 chars)>
```

The timestamp prefix gives natural chronological ordering in directory listings. The slug appendix is a human hint — agents read it in `agent-memory status` to recognise pending proposals.

## What never gets logged

Same rule as the secret scanner: **the orchestrator never echoes matched secret bytes.** `rejectWithFindings` populates `ProposeResponse.Findings` with `Type` + `Line` + `ApproximateLocation` only — never the raw bytes that triggered the rule. Downstream loggers and the MCP serialiser must use these fields, not re-slice the original content.

## What the orchestrator does NOT do

Out of scope for T3.7 / T3.10:

- **Git operations** — auto-stage / commit on apply land in T3.8.5 with the M4 git module.
- **Index full rebuild** — `applyImmediately` re-indexes only touched files; structural index repair is `rebuild-index`.
- **Staging GC** — TTL enforcement (`manifest.staging.ttl_seconds`) is the M5 review-CLI's responsibility.
- **Schema cross-section validation** — required top-level headings, ID uniqueness across the file are still M3 batch 4 / M5.
- **Real-time conflict detection** — the cross-process lock is enough for M3; merge-driver / rebase support is M4.

## References

- [Design Doc v0.4.1 §22 (propose_update tool)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.2 T3.7 / T3.8 / T3.10](../../agent-memory-implementation-plan.md).
- [Pattern: Security Layer](security-layer.md) — the secret scanner + allowlist + provenance validators this pipeline composes.
- [Pattern: Byte-Preserving Engine](byte-preserving-engine.md) — the splice primitive sequential planning depends on.
- [Pattern: Cross-Process Locking](cross-process-locking.md) — the advisory lock the orchestrator holds.
