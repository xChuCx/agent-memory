# Project agents

This file is read by AI coding agents (OpenAI Codex CLI, Cursor's
agent mode, Sourcegraph Cody, and others) as persistent context for
this project.

## Memory layer (agent-memory)

This project uses [`agent-memory`](https://github.com/xChuCx/agent-memory),
a local context middleware that maintains a structured, byte-preserving
Markdown memory layer for the repository. Three MCP tools are wired
into your runtime:

- **`memory.fetch_context`** — read; budgeted Markdown pack from
  current task state, conventions, decisions, modules.
- **`memory.propose_update`** — write; structured operations that the
  server validates, secret-scans, and either applies immediately or
  stages for human review.
- **`memory.status`** — health; file counts, pending staged proposals
  (with drift status), and security/git/lock posture. Read-only.

### At session start: empty-query fetch_context

```json
{ "name": "memory.fetch_context", "arguments": {} }
```

The bootstrap pack contains your last working notes on this branch,
cross-branch shared state, conventions, and an index summary. Read it
**before** reading source files. It tells you what the team already
decided, what footguns are documented, and where you left off.

### When else to fetch_context

| Trigger | Call |
|---------|------|
| Topic shift mid-task (auth → billing) | `fetch_context` with a query naming the new topic |
| Architectural decision approaching | `fetch_context` with a query naming the area |
| Refactoring an unfamiliar module | `fetch_context` with `scope: ["<module-path>"]` |

Do **not** call on every tool call. Once at session start plus
query-driven refreshes is the right cadence.

### When to propose_update

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
`staging_id` in the response and a `status: "staged"`. No further
action required from you.

### Operation kinds

| `operation` | Use for |
|-------------|---------|
| `create_file` | New file. `if_exists`: `reject` (default), `append`, `replace`. |
| `replace_section` | Rewrite a whole section (heading included). |
| `append_section` | Add a brand-new section under a parent or at EOF. |
| `append_to_section` | Append text to an existing section, heading untouched. |
| `replace_section_content` | Rewrite the body only; heading + anchor preserved. |
| `archive_section` | Copy a section to a new `archive/` file, leave a stub. Needs `archive_path` + `replacement`. Always stages. |
| `remove_section` | Archive-first removal: copy to `archive/`, then splice out. Needs `archive_path` + optional `reason`. Always stages. |
| `rename_heading` | Rename a heading (level ±1) keeping the `@id`. Needs `new_heading` + optional `new_heading_level`. |

Archive paths must be inside `archive/` and must not already exist
(archive files are write-once).

Multi-op proposals run **sequentially in memory** — op #2 sees op #1's
post-state bytes, never disk.

### Provenance (sources, confidence)

Durable categories (`decisions.md`, `modules/`, `conventions.md`)
**require** provenance:

```json
"sources": [{"type": "file", "ref": "internal/auth/session.go"}],
"confidence": "confirmed"
```

`type` ∈ {`file`, `test`, `user`, `session`, `inference`, `external`}.
`external` and `inference` are forbidden for `record_decision` —
durable decisions trace to code, tests, or the user.

`confidence` ∈ {`confirmed`, `inferred`, `user-provided`, `stale`, `unknown`}.

### Hard rules

- **NEVER put credentials in memory.** API keys, tokens, private keys,
  JWT bodies are server-rejected with `reason: secret_detected`. No
  override flag. Format examples wrap in
  `<!-- @secret-scan: allow reason="..." -->` ... `<!-- @secret-scan: end -->`.
- **NEVER phrase speculation as a decision.** Use `update_current`
  (working notes) for uncertain claims.
- **NEVER include PII** beyond what's already in source.

### Worked example: record a decision

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
        "content": "## Use Postgres for Transactional Storage\n<!-- @id: use-postgres -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nWe will use Postgres for the transactional store.\n"
      }
    ]
  }
}
```

Response: `status: "staged"`, `staging_id: "20260527T..."`.

### Worked example: log a session

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

`path` is irrelevant under `session_log` — the server rewrites it to
`sessions/<UTC-today>.md`.

### Reject reasons

If `status: "rejected"`, read the `message` field. Common codes:

| `reason` | What to do |
|----------|------------|
| `invalid_intent` | Pick a valid intent from the table above |
| `validation_failed` | Content doesn't parse / required fields missing — rewrite per message |
| `secret_detected` | Content contains a credential — rewrite without it |
| `provenance_violation` | Missing or forbidden sources for a durable category |
| `invalid_path` | Path escapes `.agent-memory/` — use a forward-slash path under it |
| `unknown_category` | No schema category matches the path |
| `lock_held` | Retry once after a short delay |
| `target_drift` (on staged apply) | Re-fetch context, re-stage with current snapshot |

Do **not** retry blind. Read the message and fix the root cause first.
