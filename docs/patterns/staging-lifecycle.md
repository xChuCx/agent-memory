# Pattern: Staging Lifecycle (review / apply / reject)

**Status:** Implemented in [`internal/memory/staging.go`](../../internal/memory/staging.go), CLI in [`internal/cli/review.go`](../../internal/cli/review.go), [`apply.go`](../../internal/cli/apply.go), [`reject.go`](../../internal/cli/reject.go).
**Owner:** `internal/memory/` + `internal/cli/` (M5 subset closing Release 0.1).
**Tracks design:** [Design Doc v0.4.1 §22 + §24](../../agent-memory-design-doc-v0.4.1.md).

## Problem

`propose_update` decides per-proposal whether to apply immediately or stage. Staged proposals sit on disk under `.agent-memory/staging/<id>/` until a human reviews them. We need:

1. A way to enumerate staged proposals (`review` no-args).
2. A way to inspect one proposal's metadata, drift targets, and post-state content (`review <id>` / `review <id> --show`).
3. A way to commit a staged proposal to live memory (`apply <id>`).
4. A way to discard one without applying (`reject <id>`).

Critically: between stage and apply the on-disk state may have changed. We MUST re-validate every `OperationTarget` against the current bytes before writing.

## Lifecycle

```
                      ┌─────────────────┐
                      │   propose_update │
                      │   (apply / stage / reject)
                      └──────┬──────────┘
                             │ routing decides stage
                             ▼
        ┌─────────────────────────────────────────────────┐
        │  .agent-memory/staging/<id>/                    │
        │    proposal.json                                │
        │    target-checksums.json                        │
        │    files/<rel-path>                             │
        └──────┬─────────────────────────────────┬────────┘
               │                                  │
   review <id> │ apply <id>          reject <id>  │
               ▼                                  ▼
    CheckDrift per target            os.RemoveAll(staging/<id>)
       │
       │ no drift
       ▼
    for each file in proposal.Files:
      WriteAtomic memory/files/<rel>
    reindex touched sections
    os.RemoveAll(staging/<id>)
```

## Drift re-validation

`CheckDrift(memDir, OperationTarget)` is the heart of safe apply. Each policy maps to a specific re-check against the now-current disk state:

| Policy | Re-check |
|--------|----------|
| `require_section_content_match` | Re-read the file, `ParseSections`, `FindByID`. If section is gone → drift. If `sec.ContentHash != t.Hash` → drift. |
| `require_section_resolvable` | Re-read, parse, `FindByID`. If gone → drift. Hash ignored — append-style ops tolerate growth. |
| `require_file_absent` | `os.Stat`. File present → drift. |
| `require_file_present` | `os.Stat`. File absent → drift. |

If ANY target drifts the apply rejects with `Reason: target_drift` and a `Drift []DriftReport` listing every mismatch. **The staging directory is left intact** — the agent can fix the conflict and re-stage without re-typing the proposal.

### Why content hash, not file mtime

The hash captures a section's exact bytes. `mtime` would change every time anything in the file is rewritten — even an unrelated section — producing false-positive drift. Section-level granularity also lets two unrelated proposals on the same file coexist safely if they target different sections.

## ApplyResult contract

`ApplyStaged` and `RejectStaged` both return `*ApplyResult, error`. The contract:

- **Go error** → infrastructure failure (lock open, JSON unparseable, destination write). The shell wrapper turns these into non-zero exit codes.
- **`ApplyResult{Status: "applied"}`** → success; bytes are on disk, staging dir is gone.
- **`ApplyResult{Status: "rejected", Reason: ...}`** → application-level rejection. `Reason` is one of the stable codes:
  - `staging_not_found` — no such id.
  - `target_drift` — disk state changed since stage.
  - `lock_held` — another writer is in the middle of something.

Rejection is NOT a Go error so the CLI can render it nicely AND the MCP wrapper (future `memory.apply_staged` tool) can stay simple — the agent sees the rejection in the response body, not as a transport error. Mirrors the contract `ProposeUpdate` already uses.

`RejectStaged` uses a distinct `Status: "rejected_by_user"` to differentiate "I deleted this on purpose" from "this couldn't be applied".

## CLI ergonomics

```
agent-memory review              # list
agent-memory review <id>         # detail
agent-memory review <id> --show  # detail + post-state file dumps
agent-memory apply <id>
agent-memory reject <id>
```

All four also accept `--json` for programmatic consumers; the structured output mirrors the in-package types verbatim.

### Exit codes

- `review`: always 0 unless `.agent-memory/` is missing.
- `apply`: 0 on `applied`, non-zero on any rejection (so shell pipelines can fail fast).
- `reject`: 0 on success, non-zero only on `staging_not_found`.

### Why `apply` returns non-zero on drift but `reject` doesn't on missing-id

A user typing `apply <id>` expects the proposal to go in. If it doesn't, the script that piped through them should know. A user typing `reject <id>` is OK with "it wasn't there" — the goal state is reached.

## Authoritative staging id

When `ListStaged` walks `staging/`, it overrides `StagedProposal.StagingID` with the directory name. The embedded JSON value is treated as informational. This means:

- If a user manually renames a staging dir (debugging, migration), downstream tools still work.
- If `proposal.json` is partly corrupted but readable, the dir name still identifies it.

The same invariant flows through `runReviewDetail` so all callers see the same id everywhere.

## What this is NOT (yet)

Out of scope for the M5 subset that closes Release 0.1:

- **TTL enforcement** — `manifest.staging.ttl_seconds` will be enforced by a separate cron-like sweeper in M5 batch 2.
- **`apply --all` / `reject --all`** — convenience flags for bulk operations.
- **Real diff rendering** — `review --show` dumps post-state content; comparing against the on-disk live file is left to the user's preferred diff tool (`agent-memory review <id> --show | diff - <(cat .agent-memory/foo.md)`).
- **Rejection log** — discarded proposals leave no trace beyond the directory being gone. A future audit log lands in M5 batch 2.

## References

- [Design Doc v0.4.1 §22 (propose_update)](../../agent-memory-design-doc-v0.4.1.md).
- [Design Doc v0.4.1 §24 (staging engine + drift detection)](../../agent-memory-design-doc-v0.4.1.md).
- [Pattern: propose_update Pipeline](propose-update-pipeline.md) — sets up everything this lifecycle consumes.
- [Pattern: Atomic Writes](atomic-writes.md) — `WriteAtomic` is the primitive apply uses to land each file.
- [Pattern: Cross-Process Locking](cross-process-locking.md) — `apply` takes the same advisory lock `propose_update` uses.
