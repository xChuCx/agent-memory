<!-- Thanks for contributing! Keep PRs focused; see CONTRIBUTING.md. -->

## What & why
Brief description of the change and the motivation. Link any issue
(`Fixes #123`).

## Changes
- ...

## Checklist
- [ ] `go build ./...`, `go test ./...`, and `go vet ./...` pass locally
- [ ] `go test -tags=e2e ./internal/e2e/...` passes if MCP/CLI behavior changed
- [ ] Tests added/updated for the behavior change
- [ ] `CHANGELOG.md` updated under `[Unreleased]`
- [ ] Docs updated (a `docs/patterns/` note if a subsystem changed)
- [ ] No secrets/PII in code, tests, or memory; logs stay on stderr
- [ ] Conventional commit subject (`feat:` / `fix:` / `docs:` / `chore:` …)
