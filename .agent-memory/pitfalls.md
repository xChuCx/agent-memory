# Pitfalls
<!-- @id: pitfalls -->

Known traps and recurring failures. Each entry: what failed, why, how to
avoid it, related files.

## MCP logs must never touch stdout
<!-- @id: mcp-stdout-jsonrpc -->

The `agent-memory mcp` server speaks JSON-RPC on **stdout**. Any log line
written there corrupts the framing and the client disconnects. The logger
is built to `os.Stderr` in `mcp.New`; keep it that way. The e2e smoke test
parses stdout, so a regression here fails CI.
**Files:** internal/mcp/server.go, internal/logging/logging.go

## gofmt/CRLF noise on Windows
<!-- @id: gofmt-crlf-windows -->

`gofmt -l` flags many files locally because the Windows working tree is
CRLF while `.gitattributes` stores LF. This is NOT a CI failure: the
default golangci-lint linter set excludes gofmt, and CI checks out LF.
Verify your OWN changes are gofmt-clean on an LF-normalized copy
(`tr -d '\r' | gofmt -l`); don't reformat unrelated pre-existing drift.
**Files:** .gitattributes, .github/workflows/ci.yml

## staticcheck catches what local build/test miss
<!-- @id: staticcheck-ci-vs-local -->

CI runs golangci-lint (install-mode goinstall) whose staticcheck pass
flags SA-series issues (SA4004/SA4008 no-op loops, SA9003 empty branches)
that `go build` and `go test` happily accept. Historically these caused
red CI after green local runs. Install golangci-lint locally to pre-check,
or review new code for empty `if` bodies and single-iteration loops.
**Files:** .github/workflows/ci.yml

## install --root must be absolute
<!-- @id: install-relative-root -->

`WriteAtomic` rejects relative paths. `install <adapter> --root .` used to
fail with `path must be absolute: "GEMINI.md"` because a relative root was
passed straight to the adapter. `runInstall` now resolves `--root` to
absolute for every project-local adapter. Any new path that reaches
WriteAtomic must be absolute.
**Files:** internal/cli/install.go, internal/fs

## Force-stage only durable categories
<!-- @id: force-stage-durable-only -->

`create_file if_exists=replace` force-stages, but ONLY for durable
(git-tracked) categories. Scoping it wider broke local-current/sessions
auto-apply (their whole-file replace is normal). `forcedStageReason`
takes the resolved op categories so it can see git_tracked.
**Files:** internal/memory/update.go, internal/memory/routing.go
