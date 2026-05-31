# Contributing to agent-memory

Thanks for your interest! This is a pre-1.0 project; issues, discussion, and
PRs are all welcome. By contributing you agree your work is licensed under
the project's [Apache License 2.0](LICENSE).

## Before you start

- For anything non-trivial, open an issue first so we can agree on the
  approach before you invest time.
- Bug reports: include `agent-memory version`, your OS, and minimal repro
  steps. Security issues go through [SECURITY.md](SECURITY.md), not public
  issues.

## Development setup

```bash
# Go 1.25+ (see go.mod). No CGo — the SQLite driver is pure Go.
go build ./...                       # build everything
go build -o bin/agent-memory ./cmd/agent-memory

go test ./...                        # unit + integration
go test -tags=e2e ./internal/e2e/... # end-to-end (spawns the MCP server)
go vet ./...                         # includes the slog key/value check
```

CI runs `lint` (golangci-lint), `test` on ubuntu/macos/windows, `e2e` on
linux, and a goreleaser config check. Please get `go build`, `go test`, and
`go vet` green locally before pushing.

`golangci-lint` is not required locally, but it catches what `go vet` won't
(staticcheck SA-series). If you can, run it:

```bash
golangci-lint run
```

## House conventions

These are enforced by review (and documented in `.agent-memory/conventions.md`,
which this project keeps with its own tool):

- **Code and docs in English.** Commit messages too.
- **CGo-free.** No `import "C"`, no cgo-only dependencies — it keeps the
  release binaries static.
- **Forward-slash paths internally**, even on Windows. Use `path.Match`
  (not `path/filepath.Match`) for globs.
- **All logs go to stderr.** The MCP server speaks JSON-RPC on stdout; a
  stray stdout write corrupts the protocol. Never log secret/PII bytes —
  reason codes and counts only.
- **Byte-preserving Markdown.** Memory edits locate a section via the
  goldmark AST and splice bytes; never round-trip Markdown through a
  renderer.
- **Line endings are LF** (enforced via `.gitattributes`). On Windows the
  working tree may be CRLF; that's fine — `gofmt -l` noise from CRLF is not
  CI-enforced. Keep new code `gofmt`-clean; don't reformat unrelated files.
- **Tests for behavior changes.** New ops, signals, or fixes land with
  tests. Many subsystems also have a short doc under `docs/patterns/`.

## Commit & PR style

- Conventional-style subject: `feat(fetch): …`, `fix(install): …`,
  `obs(logging): …`, `docs: …`, `chore: …`. Imperative mood, ≤ ~72 chars.
- Explain the *why* in the body for non-obvious changes; update
  `CHANGELOG.md` under `[Unreleased]`.
- Keep commits focused; one logical change per commit where practical.
- Rebase on the latest `main` before opening the PR; ensure CI is green.

## Project layout

```
cmd/agent-memory      CLI entry point
internal/memory       orchestrator: propose/apply/rebase/staging, security, fetch, index_gen, jaccard
internal/mcp          stdio MCP server (3 tools)
internal/cli          cobra command tree
internal/index        SQLite FTS5 shadow index + ranking
internal/markdown     byte-preserving section engine
internal/{config,schema,logging,git,lock,fs,adapters}
docs/patterns         per-subsystem design notes
.agent-memory/        the project's own memory store (dogfooded)
```

A good first read is [docs/patterns/](docs/patterns/) and the current
[design doc](agent-memory-design-doc-v0.4.1.md).
