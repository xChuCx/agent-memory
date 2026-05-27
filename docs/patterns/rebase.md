# Pattern: rebase — Re-plan a Staged Proposal Against a New Base (M7)

**Status:** Implemented in [`internal/memory/rebase.go`](../../internal/memory/rebase.go), CLI in [`internal/cli/rebase.go`](../../internal/cli/rebase.go).
**Owner:** `internal/memory/` + `internal/cli/` (M7, Release 0.2).
**Tracks design:** [Design Doc v0.4.1 §24.3](../../agent-memory-design-doc-v0.4.1.md) (staging engine — drift recovery).

## Problem

A staged proposal carries `target-checksums.json` — a snapshot of the
disk state the agent planned against. Between stage and apply, the
disk may change: a human edits a section, another apply lands first,
a manual git pull reorganises a file. `apply <id>` then refuses with
`reason: target_drift`. The user has two choices:

1. `agent-memory reject <id>` — throw away the proposal, ask the
   agent to re-stage from scratch.
2. **`agent-memory rebase <id>`** — try to re-plan the same operations
   against the now-current bytes, write fresh staged files, then
   `apply` again.

Re-staging from scratch costs the agent another context-loaded
session and risks losing the rationale and provenance that were
already captured at first stage. Rebase is the lightweight recovery
path: same intent, same operations, refreshed planning target.

## Hard block vs soft drift

`CheckDrift` reports drift for any condition where current disk state
doesn't match what the staged target wanted. Rebase classifies each
drift:

| Policy | Drift cause | Rebase verdict |
|--------|-------------|----------------|
| `require_file_absent` | file appeared | **HARD** — semantic mismatch; the agent expected to create a new file |
| `require_file_present` | file gone | **HARD** — the file we wanted to update is missing |
| `require_section_resolvable` | section missing | **HARD** — the section we wanted is gone |
| `require_section_content_match` | section missing | **HARD** (same as above) |
| `require_section_content_match` | section still resolves, only hash differs | **SOFT** — re-plannable with `--force` |

**Hard block** means rebase cannot recover and the proposal must be
rejected. **Soft drift** means the section ID still resolves; we can
re-plan the operations against the new section content, but doing so
implicitly accepts the new base as the planning input. `--force`
makes that acceptance explicit.

## Why `--force` is mandatory for soft drift

If a `replace_section` op was planned against base content A and now
the section has content A′, re-planning produces a splice that wipes
A′ and writes the agent's Content. That might be exactly right
(content drift was a cosmetic typo fix that the proposal would
correctly overwrite). It might also be wrong (the drift represented
substantive new information the proposal didn't know about).

We can't tell automatically. `--force` is the user's "I've looked at
the diff and accept the new base as the planning input" ack. Without
it, rebase prints a diagnostic and exits non-zero so scripts can
gate on a human check.

## Pipeline

```
RebaseStaged(stagingID, force)
├─ acquire .agent-memory/meta/lock with manifest WaitTimeout
├─ load proposal.json + target-checksums.json
├─ CheckDrift on every target:
│     classify into hardBlocks + softs
├─ no drift              → return skipped_clean
├─ any hardBlock         → return rejected unresolvable_drift + drift report
├─ all soft, no --force  → return rejected force_required + drift report
└─ re-plan:
      for each unique file in proposal.Request.Operations (input order):
        read current disk bytes
        for each op:
          ParseOperation → Validate(schema) → Plan(cur) → Splice
        ValidateMarkdown(post)
        Scan(post) ← reject on any new secret
      WriteAtomic each new staged file
      Refresh content_match Hash fields from current disk
      WriteAtomic the new target-checksums.json
      return rebased
```

Provenance and routing are NOT re-checked: the original proposal
already passed them at stage time and rebase doesn't change `sources`,
`confidence`, or category routing.

## Re-splice security

The re-splice phase is exactly the same pipeline ProposeUpdate uses
on the apply path — including the secret scan. A malicious or
accidental external edit that injects a credential into the base
file will produce a post-state that contains it; the scan rejects
with `reason: rebase_secret_detected` and **no staged files are
written**. This means the recovery loop is as safe as the original
propose path — there's no way to launder a secret through rebase.

## What rebase does NOT do

- **Does NOT apply to disk.** Rebase only updates the staging area;
  the user still runs `agent-memory apply <id>` afterwards. This
  preserves the user's "is this what I want?" decision point.
- **Does NOT reset `staged_at`.** The TTL clock keeps ticking from
  the original stage time. A proposal stale enough to be swept will
  still be swept after rebase — that's correct: rebase is a fix-up,
  not a fresh stage.
- **Does NOT touch the rejection audit log.** Rebase is recovery,
  not discard. Audit log entries record proposals that were
  abandoned (`user_rejected` / `ttl_expired`).
- **Does NOT re-check routing or provenance.** The original proposal
  already passed; rebase only refreshes the base bytes that
  operations planned against.
- **Does NOT support partial rebase.** It's all-or-nothing per
  proposal. If one file is soft-rebaseable and another has hard drift,
  the entire rebase rejects.

## Recovery loop

The intended end-to-end flow:

```
$ agent-memory apply 20260527T120000-record-decision-foo
Apply REJECTED for 20260527T120000-record-decision-foo
  reason:  target_drift
  drift:   decisions.md (section: foo)
             expected: sha256:abc...
             found:    sha256:def...

$ agent-memory rebase 20260527T120000-record-decision-foo
Rebase REJECTED for 20260527T120000-record-decision-foo
  reason:  force_required
  drift:   decisions.md (section: foo)
             ...

  Re-run with --force to accept the new base content as planning input.

# (user diffs the section, decides the rebase is sane)

$ agent-memory rebase 20260527T120000-record-decision-foo --force
Rebased 20260527T120000-record-decision-foo (1 file(s)):
  re-spliced: decisions.md
Run `agent-memory apply` to land the proposal.

$ agent-memory apply 20260527T120000-record-decision-foo
Applied staging 20260527T120000-record-decision-foo
  wrote: decisions.md
Staging directory removed.
```

## When to prefer reject over rebase

- The base content drifted in a way that **changes what the proposal
  means**. Rebase would silently keep the original intent.
- The original rationale or sources are no longer valid given the
  new base. Re-stage from scratch with updated sources.
- Multiple proposals are queued and rebase ordering would matter —
  easier to reject and let the agent redo with current context.
- You just want to clean up. `reject` writes an audit-log entry;
  rebase doesn't.

## References

- [Design Doc v0.4.1 §24.3 (drift recovery)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.7 M7](../../agent-memory-implementation-plan.md).
- [Pattern: Staging Lifecycle](staging-lifecycle.md) — `apply` and
  `reject` flows that this complements.
- [Pattern: propose_update Pipeline](propose-update-pipeline.md) —
  shares the re-splice + re-validate machinery.
