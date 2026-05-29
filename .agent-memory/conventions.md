# Conventions
<!-- @id: conventions -->

Project-wide working rules for agent-memory. Code and docs in English;
chat/PR discussion may be in Russian.

## Build, test, and local checks
<!-- @id: build-test -->

- Build everything: `go build ./...`. Build the CLI binary:
  `go build -o bin/agent-memory.exe ./cmd/agent-memory`.
- Test everything: `go test ./...`. The end-to-end suite is build-tagged:
  `go test -tags=e2e ./internal/e2e/...`.
- Toolchain is `go 1.25.0` (see go.mod); local dev may use a newer Go.
- `golangci-lint` is **not** installed locally by default; CI runs it. To
  pre-check style, install it and run `golangci-lint run`. `go vet ./...`
  catches a useful subset (including the `slog` key/value check).

## Language, paths, and style
<!-- @id: language-style -->

- Go, CGo-free. No cgo anywhere — it keeps cross-compiled binaries static.
- Internal paths are forward-slash, even on Windows. Use `path.Match`
  (NOT `path/filepath.Match`) for glob matching so behaviour is identical
  across OSes.
- Filesystem writes go through `internal/fs.WriteAtomic`, which **requires
  an absolute path** (write to temp + rename). Resolve user-supplied roots
  to absolute before handing paths to it.

## Logging and security
<!-- @id: logging-security -->

- All logs go to **stderr**. The `mcp` server speaks JSON-RPC on stdout;
  a single stray stdout log line corrupts the protocol. CLI commands
  reserve stdout for their own output (packs, `--json`).
- Logging is quiet by default (WARN); opt in with `--log-level` or
  `AGENT_MEMORY_LOG=debug|info|warn|error`.
- **Never log secret or PII bytes.** Logs carry stable reason codes and
  counts only. `Finding` carries no matched bytes; never re-slice content
  by a finding's offset for a message. The raw fetch query is never logged.

## Git, CI, and commits
<!-- @id: git-ci -->

- `.gitattributes` forces LF for text files; the working tree may be CRLF
  on Windows. `gofmt -l` noise from CRLF/comment-drift is expected locally
  and is **not** CI-enforced (default golangci-lint linter set excludes
  gofmt). Keep new code gofmt-clean; don't churn unrelated pre-existing drift.
- CI jobs: `lint`, `test` (ubuntu/macos/windows), `e2e (linux)`,
  `goreleaser check`. Releases are tag-driven via goreleaser; the version
  is stamped into `internal/cli.ProgramVersion` via ldflags.
- Commit messages end with the trailer:
  `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Per-developer machine config (`.mcp.json`, `*.exe`,
  `.claude/settings.local.json`) is git-ignored.

## Markdown engine rules
<!-- @id: markdown-rules -->

- The memory layer is **byte-preserving**: edits locate a section via the
  goldmark AST, then splice bytes. Never round-trip Markdown through a
  renderer — it would reflow untouched content.
- Every durable section carries an `<!-- @id: ... -->` anchor; sections
  are the unit of indexing, fetching, and editing. Duplicate heading text
  is disambiguated by `@id` (or a 1-based occurrence counter).
