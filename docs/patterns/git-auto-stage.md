# Pattern: Git Auto-Stage on Apply (M4)

**Status:** Implemented in [`internal/git/commit.go`](../../internal/git/commit.go), [`internal/memory/autostage.go`](../../internal/memory/autostage.go).
**Owner:** `internal/git/` + `internal/memory/` (M4, Release 0.2).
**Tracks design:** [Design Doc v0.4.1 §27](../../agent-memory-design-doc-v0.4.1.md) (Git integration).

## Problem

When an apply lands — either through direct `apply_immediately` (e.g., a
`session_log` or `add_pitfall` append) or through `agent-memory apply
<staging-id>` — the resulting bytes sit in the working tree but are
**not** in git's index. The user has to remember to `git add` them
manually. Friction multiplies across a day:

- Decisions silently fail to land in the same commit as the code change
  that motivated them.
- A `session_log` accumulating in `sessions/2026-05-27.md` shows up as
  noisy "Untracked / Modified" in every `git status` until the user
  acts.
- An agent that "did the right thing" still leaves cleanup work for the
  human.

## Solution

Two manifest flags + four lines of orchestration:

```yaml
git:
  auto_stage_changes: false   # default: do nothing automatic
  auto_commit: false          # apply auto_stage_changes first
  commit_message_prefix: "chore(memory):"
  track_local: false          # include local/* in the auto-stage
  track_sessions: false       # include sessions/* in the auto-stage
```

Inside the orchestrator's apply paths (`applyImmediately` and
`ApplyStaged`), after the post-state files are WriteAtomic'd and
re-indexed, `maybeAutoStage` runs:

```
maybeAutoStage(deps, repoRoot, files, intent, rationale)
├─ feature-gated on manifest.git.auto_stage_changes
├─ for each file:
│     if shouldStage(file, schema, gitCfg) → include in `git add`
├─ git add — <files...>
└─ if manifest.git.auto_commit:
      git commit -m "<prefix> <intent> — <rationale>\n\nFiles: ..."
```

The result rides back on `ProposeResponse.AutoStage` (or
`ApplyResult.AutoStage` for staged-apply) so the CLI + MCP tool can
surface what happened to the user.

## `shouldStage` policy

Per-file decision rule, checked in order:

1. The file's schema category has `git_tracked: true` → **stage**.
2. The file lives under `local/` AND `manifest.git.track_local: true` →
   **stage** (override the default-untracked behaviour).
3. The file lives under `sessions/` AND `manifest.git.track_sessions:
   true` → **stage**.
4. Otherwise → **skip**.

Files matched by no schema category — e.g., a user-dropped note in a
folder agent-memory doesn't know about — are always skipped. Auto-stage
is conservative: we only stage files we know we wrote.

## Error contract

Auto-stage runs **after** the file bytes are durable on disk. By
construction, no auto-stage failure can roll back the apply. Errors are
collected into `AutoStageResult.Errors` and surfaced to the caller, but
never escalated:

- Missing `git` binary → `Skipped: true`, no error. Plenty of projects
  don't use git; that's not a failure mode.
- Project root isn't a git work tree → `Skipped: true`, no error.
- `git add` fails (locked index, permissions) → error captured;
  `Staged` empty; the rest of the apply already succeeded.
- `git commit` fails (no `user.email` configured, pre-commit hook
  rejected) → error captured; `Staged` still reflects what was added;
  the user can commit manually.
- Nothing actually got staged after the filter → `Skipped: true`. Not
  an error: an apply to `local/current.<branch>.md` with `track_local:
  false` is correctly silent.

## What auto-stage does NOT do

By deliberate design:

- **Never `git add .`** — the file list is explicit; one rogue
  side-effect cannot pull in unrelated changes.
- **Never `git push`** — local-only behaviour. Pushing is the user's
  decision, ideally bundled with whatever code change motivated the
  memory update.
- **Never `--no-verify`** — pre-commit hooks run as configured. A
  user with a 5-second formatter hook now waits 5 seconds per apply;
  toggle `auto_commit: false` if that's untenable.
- **Never `git reset` / `git checkout --` / any destructive op** — the
  apply path's only git verbs are `add` and `commit`.
- **Never amend or rewrite history** — every apply is a fresh commit.
  This keeps `git log` honest about what landed when.

## Commit message format

```
<prefix> <intent> — <rationale-or-empty>
                                                   ← blank line
Files: <comma-separated repo-rooted forward-slash paths>
```

Example:

```
chore(memory): record_decision — use postgres for transactional storage

Files: .agent-memory/decisions.md
```

The prefix defaults to `chore(memory):` (settable via
`manifest.git.commit_message_prefix`). The intent uses the wire enum
value (`record_decision`, `session_log`, `add_pitfall`, …) so a
mechanical reader can group commits by category.

## Defaults

`config.DefaultManifest` ships with `auto_stage_changes: false` and
`auto_commit: false`. M4 is **opt-in** — existing v0.1 deployments
upgrade with zero behavioural change. The flags appear in
`manifest.yaml` for users to enable when ready.

For first-time users, the recommended progression:

1. Try `auto_stage_changes: true, auto_commit: false` for a day —
   observe what lands in `git status` after every apply.
2. If happy with the staged file list, flip `auto_commit: true`. Each
   apply now produces its own commit.
3. If the commit cadence is too granular, leave `auto_commit: false`
   and squash the `git add`'d files into your normal commits.

## Integration points

| Caller | Apply path | Auto-stage runs |
|--------|------------|-----------------|
| `agent-memory mcp` → `memory.propose_update` with apply-routing intent | `applyImmediately` | yes |
| `agent-memory apply <staging-id>` | `ApplyStaged` | yes |
| Direct `memory.ProposeUpdate(...)` Go calls | both above | yes |
| Stage path of `propose_update` (rejection / staging dir write) | n/a | no — nothing landed yet |

A stage path doesn't auto-commit the staging directory itself.
Staging artefacts are intentionally **not** git-tracked (the manifest
ships with no `.gitignore` entry for them, but they live under
`.agent-memory/staging/` which most projects exclude). If a user wants
to track staging history for audit, that's a separate manual choice.

## What's still out of scope (Release 0.2+ stretch)

- **Per-category commit message templates** — currently the message
  format is one-size-fits-all. A future enhancement could let
  `schema.yaml` declare a per-category template (e.g.,
  decisions get a richer message body with sources cited).
- **Push integration** — `--push` on `agent-memory apply`, or a
  manifest flag. The use cases for which "auto-push" is correct are
  narrow; we'd want explicit user consent every time.
- **Signed commits** — relies entirely on the user's
  `commit.gpgsign` / `commit.gpgSign` config; not surfaced by
  agent-memory.

## References

- [Design Doc v0.4.1 §27 (Git integration)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.4 M4](../../agent-memory-implementation-plan.md).
- [Pattern: propose_update Pipeline](propose-update-pipeline.md) — the
  apply path auto-stage hooks into.
- [Pattern: Staging Lifecycle](staging-lifecycle.md) — `ApplyStaged`
  invokes the same `maybeAutoStage` after writing.
