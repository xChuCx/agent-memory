# Gemini project memory

This file is read by Gemini CLI as long-term project context for
every session in this repository.

## Memory layer: agent-memory

This project uses [`agent-memory`](https://github.com/xChuCx/agent-memory),
a local context middleware that maintains a structured, byte-preserving
Markdown memory layer. Two MCP tools are wired into your runtime:

- **`memory.fetch_context`** — read; budgeted Markdown pack assembled
  from current task state, conventions, decisions, modules.
- **`memory.propose_update`** — write; structured operations that
  the server validates, secret-scans, and either applies immediately
  or stages for human review.

## At session start: empty-query fetch_context

```json
{ "name": "memory.fetch_context", "arguments": {} }
```

The bootstrap pack contains your last working notes on this branch,
cross-branch shared state, conventions, and an index summary. Read
it **before** reading source files.

## When else to call fetch_context

| Trigger | Call |
|---------|------|
| Topic shift mid-task (auth → billing) | `fetch_context` with a query naming the new topic |
| Architectural decision approaching | `fetch_context` with a query naming the area |
| Refactoring an unfamiliar module | `fetch_context` with `scope: ["<module-path>"]` |

Do **not** call on every tool call. Once at session start plus
query-driven refreshes is the right cadence.

## When to call propose_update

| Situation | `intent` | Routes to |
|-----------|----------|-----------|
| Working notes on this task, branch-scoped | `update_current` | apply |
| Working notes that follow you across branches | `update_shared` | apply |
| End-of-task log of what you did | `session_log` | apply (path auto-rewritten to `sessions/<UTC today>.md`) |
| Hit a footgun future-you should avoid | `add_pitfall` | apply (`append_to_section`) / stage (rewriting) |
| Made a durable architectural decision | `record_decision` | **stage** |
| Updated facts about a module | `refresh_module` | **stage** |
| Discovered a team convention | `update_conventions` | **stage** |
| An older entry is no longer accurate | `archive_stale` | **stage** |

"Stage" means the user reviews before it lands. You'll get a
`staging_id` in the response. No further action required from you.

## Operation kinds (`operations[]`)

| `operation` | Use for |
|-------------|---------|
| `create_file` | New file. `if_exists`: `reject` (default), `append`, `replace`. |
| `replace_section` | Rewrite a whole section (heading included). |
| `append_section` | Add a brand-new section under a parent or at EOF. |
| `append_to_section` | Append text to an existing section, heading untouched. |
| `replace_section_content` | Rewrite the body only; heading + anchor preserved. |

Multi-op proposals run **sequentially in memory** — op #2 sees op
#1's post-state bytes, never disk.

## Provenance

Durable categories (`decisions.md`, `modules/`, `conventions.md`)
**require** provenance:

```json
"sources": [{"type": "file", "ref": "internal/auth/session.go"}],
"confidence": "confirmed"
```

`type` ∈ {`file`, `test`, `user`, `session`, `inference`, `external`}.
`external` and `inference` are forbidden for `record_decision`.

`confidence` ∈ {`confirmed`, `inferred`, `user-provided`, `stale`, `unknown`}.

## Hard rules

- **NEVER put credentials in memory.** Server rejects with
  `reason: secret_detected`. No override flag. Format examples wrap
  in `<!-- @secret-scan: allow reason="..." -->` ...
  `<!-- @secret-scan: end -->`.
- **NEVER phrase speculation as a decision.** Use `update_current`
  for uncertain claims.
- **NEVER include PII** beyond what's already in source.

## Reject reasons

If `status: "rejected"`, the `message` field tells you what to fix.
Common codes:

| `reason` | What to do |
|----------|------------|
| `invalid_intent` | Pick a valid intent from the table above |
| `validation_failed` | Rewrite per the message |
| `secret_detected` | Rewrite without the credential |
| `provenance_violation` | Add valid `sources[]`; avoid `external`/`inference` for `record_decision` |
| `invalid_path` | Use a forward-slash path under `.agent-memory/` |
| `lock_held` | Retry once after a short delay |
| `target_drift` (on staged apply) | Re-fetch context, re-stage |

Do **not** retry blind. Read the message and fix the root cause first.

## Examples

### Record a decision

```json
{
  "name": "memory.propose_update",
  "arguments": {
    "intent": "record_decision",
    "rationale": "use postgres for transactional storage",
    "sources": [{"type": "user", "ref": "design-meeting-2026-05-27"}],
    "confidence": "confirmed",
    "operations": [
      {
        "operation": "append_section",
        "path": "decisions.md",
        "heading": "Use Postgres for Transactional Storage",
        "heading_level": 2,
        "content": "## Use Postgres for Transactional Storage\n<!-- @id: use-postgres -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nWe will use Postgres.\n"
      }
    ]
  }
}
```

### Session log

```json
{
  "name": "memory.propose_update",
  "arguments": {
    "intent": "session_log",
    "operations": [
      {
        "operation": "create_file",
        "path": "_",
        "if_exists": "append",
        "content": "# 2026-05-27\n\n## Session: auth refactor\n\n- Moved session validation into middleware.\n"
      }
    ]
  }
}
```
