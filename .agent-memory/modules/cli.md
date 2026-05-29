# Module: internal/cli
<!-- @id: module-cli -->

The cobra command tree for the `agent-memory` binary. Each command resolves
deps (manifest/schema/index) and calls into `internal/memory`. Stdout is
reserved for command output; logs go to stderr via `cliLogger()`.

## command surface
<!-- @id: cli-commands -->

`init`, `status`, `doctor`, `fetch`, `mcp`, `review`, `apply`, `reject`,
`sweep`, `rebase`, `rebuild-index`, `install <adapter>`, `version`. There
is intentionally **no** write command — agents propose via MCP; humans use
the staging lifecycle commands. Persistent flag `--log-level` sets stderr
verbosity. `apply`/`reject`/`rebase` accept a full staging id, a unique
prefix, or `--latest`.
**Sources:** internal/cli/root.go

## adapters install
<!-- @id: cli-install -->

`install <adapter>` writes an integration file teaching a host agent to
use the tools: `claude` (.claude/skills/agent-memory/SKILL.md),
`cursor` (.cursor/rules/agent-memory.mdc), `agents` (AGENTS.md),
`gemini` (GEMINI.md). `--root` is resolved to absolute before dispatch
(WriteAtomic requires absolute paths). claude/cursor support
`--user-global`; agents/gemini are project-only.
**Sources:** internal/cli/install.go, internal/adapters
