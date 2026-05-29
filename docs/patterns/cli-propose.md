# Pattern: CLI `propose` (human write path)

**Status:** Implemented in [`internal/cli/propose.go`](../../internal/cli/propose.go).
**Owner:** `internal/cli/` (human-facing front door).
**Tracks design:** [Design Doc v0.4.1 §15.2](../../agent-memory-design-doc-v0.4.1.md) (propose_update).

## Problem

Memory writes go through `memory.ProposeUpdate` — validation, secret/PII
scan, provenance, routing, apply-vs-stage. The only caller used to be the
MCP `memory.propose_update` tool, so a developer without an MCP server
running had no first-class way to add a decision/pitfall/convention: they'd
hand-edit Markdown (minding `@id` anchors and section schema) and run
`rebuild-index`, bypassing every safety check.

## Solution

`agent-memory propose` is a thin CLI front door to the **same**
`ProposeUpdate` pipeline the MCP tool uses. Nothing about validation,
secret scanning, provenance, or routing differs — only the transport.

```sh
# common single-operation case, via flags
agent-memory propose --intent add_pitfall \
  --op append_to_section --path pitfalls.md --section-id lock-ordering \
  --content "- always lock A before B"

# full control (multi-op, exact fields), via JSON on stdin
cat req.json | agent-memory propose --from-json -
```

Two input modes:

- **Flags** build a one-operation `ProposeRequest`. Content comes from
  `--content` (literal), `--content-file <path>`, or `--content-file -`
  (stdin). Provenance via repeatable `--source type:ref`.
- **`--from-json <file|->`** decodes a full `ProposeRequest` (with
  `DisallowUnknownFields`, so typos fail loudly), for multi-operation
  proposals or exact field control. Overrides the flag-based operation.

Output is human-readable or `--json` (a `ProposeResponse`, plus an
`applied` block when `--apply` ran). Exit status is non-zero on rejection,
so scripts can fail fast — mirrors `apply`.

## `--apply`: the human is the reviewer

Routing still decides apply-vs-stage by category: durable categories
(`decisions`, `modules`, `conventions`, `archive`) **stage** for review;
local/session notes and pitfall appends **apply**. When a developer runs
`propose` interactively they ARE the reviewer, so `--apply` immediately
applies a result that would otherwise stage:

```text
ProposeUpdate → status=staged, staging_id=…
   └─ if --apply: ApplyStaged(staging_id)   ← drift re-check, index update, git auto-stage
```

This composes the existing `ApplyStaged` (no orchestrator change) — the
proposal is staged, then the same code path `agent-memory apply` uses lands
it. Without `--apply`, the staged proposal waits for
`agent-memory review <id> --diff` → `agent-memory apply <id>`.

The agent-facing safety model is untouched: agents writing through MCP
never get `--apply`; the human review gate on durable categories stays.

## Why not a separate "force apply" flag in the orchestrator

`ProposeUpdate` stays pure: it proposes and routes, full stop. Auto-apply
is a *caller* policy ("I trust this, land it now"), so it lives in the CLI
as a post-step, not as a `ForceApply` field threaded through routing.
Keeps the orchestrator's apply-vs-stage decision the single source of truth
and reuses the audited apply path verbatim.

## Tests

[`internal/cli/propose_test.go`](../../internal/cli/propose_test.go): flag
apply (pitfall) / flag stage (decision) / `--apply` lands a staged decision
/ `--from-json` over stdin / provenance rejection (not a Go error) /
required-flag errors / non-zero exit through the cobra command.

## References

- [Design Doc v0.4.1 §15.2](../../agent-memory-design-doc-v0.4.1.md) — propose_update contract.
- [propose-update-pipeline.md](./propose-update-pipeline.md) — the shared pipeline.
- [staging-lifecycle.md](./staging-lifecycle.md) — review/apply/reject the CLI feeds.
