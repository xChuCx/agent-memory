---
name: agent-memory
description: Use this skill whenever working on a project that has a .agent-memory/ directory. At the start of every coding task call memory.fetch_context to load current state, conventions, and relevant prior decisions. After making a durable choice, hitting a footgun, or wrapping a session, call memory.propose_update to persist what was learned. Always prefer this over re-reading raw source files when you need project context.
---

# agent-memory

`agent-memory` is a local context middleware that maintains a structured,
byte-preserving Markdown memory layer for this repository. Your runtime
exposes three MCP tools backed by it:

- **`memory.fetch_context`** — read; returns a budgeted Markdown pack
  drawn from current task state, conventions, decisions, modules, and
  (on query) the most relevant indexed sections.
- **`memory.propose_update`** — write; submits structured operations
  that the server validates, secret-scans, and either applies
  immediately or stages for human review.
- **`memory.status`** — health; reports file counts, pending staged
  proposals (with drift status), and security/git/lock posture. Call
  it when you want to know whether memory needs maintenance before
  proposing more updates.

## At session start: always fetch_context with empty query

```json
{ "name": "memory.fetch_context", "arguments": {} }
```

The empty-query (“bootstrap”) pack contains:

- `local/current.<branch>.md` — your last working notes on this branch.
- `local/current.shared.md` — cross-branch shared state.
- `conventions.md` — project conventions.
- `index.md` — summary of the memory layout.

Read it carefully **before** reading source files. It tells you what the
team already decided, what footguns are documented, and where you left
off.

## When else to fetch_context

| Trigger | Call |
|---------|------|
| Topic shift mid-task (auth → billing) | `fetch_context` with a query naming the new topic |
| About to make an architectural decision | `fetch_context` with a query naming the area |
| Refactoring an unfamiliar module | `fetch_context` with `scope: ["<module-path>"]` |

Do **not** call `fetch_context` on every tool call. Once at session start
plus query-driven refreshes is the right cadence.

## When to propose_update

Pick the intent that matches the situation. Each intent maps to a
specific category of memory file and an approval policy.

| Situation | `intent` | Routes to |
|-----------|----------|-----------|
| Working notes on this task, branch-scoped | `update_current` | apply |
| Working notes that follow you across branches | `update_shared` | apply |
| End-of-task log of what you did | `session_log` | apply (path auto-rewritten to `sessions/<UTC today>.md`) |
| Hit a footgun future-you should avoid | `add_pitfall` | apply (when `append_to_section`) / stage (when rewriting) |
| Made a durable architectural decision | `record_decision` | **stage** |
| Updated facts about a module | `refresh_module` | **stage** |
| Discovered a team convention | `update_conventions` | **stage** |
| An older entry is no longer accurate | `archive_stale` | **stage** |

“Stage” means the user reviews the proposal before it lands. You'll
get a `staging_id` in the response and a `status: "staged"`. No further
action required from you.

## Operation kinds

Pass these in the `operations[]` array. Most proposals use ONE operation;
multi-op proposals run **sequentially in memory** (op #2 sees op #1's
post-state bytes — never disk).

| `operation` | Use for |
|-------------|---------|
| `create_file` | New file. `if_exists`: `reject` (default), `append`, `replace`. |
| `replace_section` | Rewrite a whole section (heading included). Use anchored sections (`<!-- @id: ... -->`) when possible. |
| `append_section` | Add a brand-new section under a parent or at EOF. |
| `append_to_section` | Append text to an existing section without touching its heading. Best for bullets and incremental logs. |
| `replace_section_content` | Rewrite the body only; heading + anchor preserved. |

## Provenance (sources, confidence)

Durable categories (`decisions.md`, `modules/`, `conventions.md`)
**require** provenance. Pass:

```json
"sources": [{"type": "file", "ref": "internal/auth/session.go"}],
"confidence": "confirmed"
```

`type` ∈ {`file`, `test`, `user`, `session`, `inference`, `external`}.
For `record_decision`, `external` and `inference` are forbidden — durable
decisions must trace back to code, tests, or the user.

`confidence` ∈ {`confirmed`, `inferred`, `user-provided`, `stale`, `unknown`}.

## What NEVER goes in memory

- **API keys, tokens, private keys, JWT bodies.** The server rejects
  with `reason: secret_detected`. There is no override flag. If you
  genuinely need to document a token *format* (not a real value),
  wrap the example in:
  ```html
  <!-- @secret-scan: allow reason="documentation: token format example" -->
  ...
  <!-- @secret-scan: end -->
  ```
- **Speculation phrased as decision.** If you're not sure, use
  `update_current` (working notes), not `record_decision`.
- **Personally identifiable user data** beyond what's already in source.

## Worked examples

### 1. Recording a decision (stages)

User and you concluded on a database choice:

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
        "content": "## Use Postgres for Transactional Storage\n<!-- @id: use-postgres -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nWe will use Postgres for the transactional store. SQLite was considered but the team prefers Postgres's operational maturity.\n"
      }
    ]
  }
}
```

Response: `status: "staged"`, `staging_id: "20260527T143012-..."`. The
user will run `agent-memory apply <id>` after reviewing.

### 2. Logging a session (applies)

End of a coding session:

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
        "content": "# 2026-05-27\n\n## Session: auth refactor\n\n- Moved session validation into middleware.\n- Pitfall: cookie SameSite default differs between dev and prod.\n- Next: write integration test for the cookie path.\n"
      }
    ]
  }
}
```

The `path` is irrelevant under `session_log` — the server rewrites it
to `sessions/<UTC-today>.md`. Response: `status: "applied"`.

### 3. Adding a pitfall as a bullet (applies)

```json
{
  "name": "memory.propose_update",
  "arguments": {
    "intent": "add_pitfall",
    "operations": [
      {
        "operation": "append_to_section",
        "path": "pitfalls.md",
        "section_id": "auth-pitfalls",
        "content": "- Cookie SameSite=Lax breaks the OAuth redirect in Safari ≥17; set to None+Secure (date: 2026-05-27, source: bug report #1842).\n"
      }
    ]
  }
}
```

`append_to_section` routes to apply (additive, low-risk). Response:
`status: "applied"`.

## When propose_update rejects

The response body has `status: "rejected"` and a stable `reason` code.
Read the `message` field — it tells you what to fix.

| `reason` | What happened | What to do |
|----------|---------------|------------|
| `invalid_intent` | Not in the closed set above | Pick a valid intent |
| `invalid_operation` | Unknown `operation` kind | See operation table above |
| `validation_failed` | Content doesn't parse / required fields missing | Rewrite per the message |
| `invalid_path` | Path escapes `.agent-memory/` or targets a derived file | Use a forward-slash path under `.agent-memory/` |
| `unknown_category` | No schema category matches the path | Move the file to a category folder |
| `server_managed_category` | You tried to write `index.md` | The server owns this; don't write to it |
| `secret_detected` | Content contains a credential | Rewrite without the secret |
| `provenance_violation` | Missing/forbidden sources for a durable category | Add valid `sources[]`; avoid `external`/`inference` for `record_decision` |
| `lock_held` | Another writer is mid-commit | Retry once after a short delay |
| `target_drift` (on staged apply) | Target changed since stage | Re-fetch context, re-stage with current snapshot |

Each rejection is informative. **Do not retry blind** — read the
message and fix the root cause first.

## Quick reference

| Tool | Required args | Optional args |
|------|---------------|---------------|
| `memory.fetch_context` | — | `query`, `scope[]`, `budget`, `include[]`, `exclude_archive` |
| `memory.propose_update` | `intent`, `operations[]` | `sources[]`, `confidence`, `rationale`, `owner` |
| `memory.status` | — | — |
