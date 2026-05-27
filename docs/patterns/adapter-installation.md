# Pattern: Adapter Installation (M6)

**Status:** Claude Code adapter implemented in [`internal/adapters/claude/`](../../internal/adapters/claude/), CLI in [`internal/cli/install.go`](../../internal/cli/install.go).
**Owner:** `internal/adapters/` + `internal/cli/` (M6 subset closing Release 0.1).
**Tracks design:** [Design Doc v0.4.1 §28](../../agent-memory-design-doc-v0.4.1.md) (Adapters).

## Problem

agent-memory exposes two MCP tools (`memory.fetch_context`,
`memory.propose_update`) but a tool catalog alone doesn't teach an agent
**when** or **how** to use them. Each agent runtime (Claude Code, Cursor,
Codex, …) has its own convention for distributing per-project agent
guidance:

- Claude Code: `.claude/skills/<name>/SKILL.md` with a YAML frontmatter
  declaring when the skill should fire.
- Cursor: `.cursor/rules/*.mdc`.
- Codex CLI: `AGENTS.md`.

Each runtime needs:

1. **An asset authored for it** — written in its native format with
   concrete examples in its expected idiom.
2. **A way to drop that asset into the user's repo** without manual
   copy-paste.

## Solution

A small package per runtime under `internal/adapters/<runtime>/`, with:

1. The embedded asset(s) authored by us (e.g. `SKILL.md` for Claude).
2. An `Install(Options) (*Result, error)` entry point that materialises
   them under the chosen layout.
3. An `AdapterName` constant the CLI uses for dispatch.

The CLI's `install` subcommand dispatches on `args[0]`:

```
agent-memory install claude            → project-local
agent-memory install claude --user-global
agent-memory install claude --force
agent-memory install claude --json
```

## Adapter contract

Every adapter package exposes:

```go
const AdapterName = "<runtime>"

type Options struct {
    Root       string // project-local install target
    UserGlobal bool   // home-dir install instead
    Force      bool   // overwrite existing assets
    HomeDir    string // test override for os.UserHomeDir()
}

type Result struct {
    Adapter string
    Files   []string // absolute paths written
    Skipped []string // paths preserved because !Force and they existed
}

func Install(opts Options) (*Result, error)
```

Rules:

- **Project-local default.** `UserGlobal: false` is the common case; the
  asset lives next to the code.
- **Idempotent.** A re-install without `--force` is a no-op that returns
  the existing paths under `Skipped`.
- **Atomic writes.** Use `internal/fs.WriteAtomic` so a partial install
  never leaves an empty file readable by the runtime.
- **Embed, don't generate.** Assets are checked in as static files and
  pulled in via `//go:embed`. Versioning, blame, and review work on them.
- **HomeDir override.** Adapters must honour `Options.HomeDir` so tests
  can install into `t.TempDir()` instead of the user's real home.

## Claude Code: SKILL.md

The Claude adapter writes one file:

```
<base>/.claude/skills/agent-memory/SKILL.md
```

`<base>` is the repo root (default) or the user's home (`--user-global`).
Claude Code auto-discovers skills under `.claude/skills/` and uses the
frontmatter `description` to decide when each skill fires.

### What the SKILL.md teaches

The asset is organised around behavior, not API reference:

1. **Frontmatter `description`** names the trigger conditions
   ("start of every coding task", "after making a durable choice", "at
   end of session"). This is what Claude Code matches against.
2. **"At session start: always fetch_context"** — the single most
   important habit; gets its own section with the explicit empty-args
   JSON example.
3. **Intent → situation table** — maps the eight intents to the
   situations that should trigger them. No prose detour; just lookup.
4. **Operation kinds reference** — `create_file`, `replace_section`,
   `append_section`, `append_to_section`, `replace_section_content` with
   one-liner use cases.
5. **Provenance rules** — what `sources`/`confidence` are, which types
   are forbidden for `record_decision`.
6. **Hard rules** — no secrets (with the allowlist-marker escape hatch),
   no speculation as decision.
7. **Three worked examples** — record_decision (stage), session_log
   (apply), add_pitfall (apply). Each is a complete JSON payload.
8. **Reject reason table** — every wire-stable code with what to do
   about it. This is the agent's debugger when its proposals fail.

The deliberate omissions: no Markdown engine internals, no FTS5
ranking math, no schema YAML reference. Those belong in the design doc
for humans; the skill teaches the agent only what changes its behavior.

### Why a single file

A skill that fans out into multiple Markdown files imposes a
discoverability cost on the runtime: it has to find them, the user has
to inspect each, and updates have to be co-ordinated. One 4 KB file
that loads atomically is faster and easier to reason about than a
directory tree pretending to be a library.

## Refusal to overwrite (no `--force`)

The default is to preserve existing assets. Rationale:

- A user might have edited the skill manually (added project-specific
  pitfalls, tightened the intent table, removed an example that
  doesn't apply).
- A version bump of `agent-memory install claude` shouldn't silently
  blow that away.
- Pairing default-preserve with an explicit `--force` keeps the
  destructive case opt-in.

The CLI prints a clear `Pass --force to overwrite.` hint when it
preserves, so the user knows the escape hatch exists.

## Result contract

`InstallResult` (CLI) wraps `Result` (adapter) one-to-one:

- `Files` non-empty → newly installed; what the runtime will pick up.
- `Skipped` non-empty → already present; nothing changed.

Both can be non-empty in future multi-file adapters (some files new,
some preserved). For Claude Code (single SKILL.md) exactly one of them
has length 1.

### Why not return error on "already installed"

Symmetric with the staging Apply contract (see [staging-lifecycle.md]
(staging-lifecycle.md)). "Already in the desired state" is not an
error; the goal was reached. Exit code is 0 so scripts don't have to
distinguish "newly installed" from "previously installed".

If the user *expects* a write and didn't get one, they pass `--force`.

## Adding a new adapter

Future runtimes (cursor, codex, gemini, …) follow the same recipe:

1. Author the asset(s) in the runtime's native format under
   `internal/adapters/<runtime>/`.
2. Add `//go:embed` and an `Install(Options) (*Result, error)` matching
   the contract above.
3. Export an `AdapterName` constant.
4. Append the constant to `supportedAdapters` in
   `internal/cli/install.go` and add a `case` to the dispatch switch.
5. Mirror the test set: project-local write, no-force no-op, force
   overwrite, user-global with HomeDir override, idempotency.

The CLI surface stays unchanged — `agent-memory install <runtime>`
works as soon as the dispatch case lands.

## What this is NOT (yet)

- **MCP server registration.** The user is expected to already have
  `agent-memory mcp` configured in their Claude Code MCP server list.
  A future `install --register-mcp` flag could write to
  `~/.claude/mcp_servers.json`, but that's out of scope for Release 0.1.
- **Uninstall.** `install` writes; the user removes the file manually
  (one file, predictable path) if they want to undo.
- **Skill version pinning.** The embedded SKILL.md is whatever was in
  the binary at build time. Users who want a specific version pin via
  the binary's version, not via a separate skill version.

## References

- [Design Doc v0.4.1 §28 (Adapters)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.5 M6](../../agent-memory-implementation-plan.md).
- [Pattern: Atomic Writes](atomic-writes.md) — Install uses `WriteAtomic`.
- [Pattern: Staging Lifecycle](staging-lifecycle.md) — same "already in
  goal state" contract.
