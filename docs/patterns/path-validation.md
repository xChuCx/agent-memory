# Pattern: Path Validation

**Status:** Implemented in [`internal/fs/paths.go`](../../internal/fs/paths.go).
**Owner:** `internal/fs/` (M1).
**Tracks design:** [Design Doc v0.4.1 §15.2](../../agent-memory-design-doc-v0.4.1.md) (pipeline step 5), §21.5.

## Problem

The agent-facing `propose_update` accepts a `path` field on every operation:

```json
{
  "operation": "replace_section",
  "path": "modules/auth.md",
  "section_id": "token-rotation",
  "content": "..."
}
```

An untrusted or buggy agent could submit paths that:

1. **Escape the `.agent-memory/` root** — e.g., `../../etc/passwd` or `modules/../../../etc/passwd`. Could expose or corrupt files outside the project's memory directory.
2. **Reference server-managed derived files** — e.g., `meta/index.sqlite` (the FTS5 shadow index) or `meta/lock` (the advisory-lock file). Direct writes to these would corrupt the system's internal state and bypass invariants the server depends on.
3. **Use absolute paths** — bypassing the relative-path contract entirely.

The validator centralises these checks so individual operation handlers never see a dangerous path.

## Solution

```go
// ValidateMemoryPath validates rel against root and returns the cleaned
// absolute path on success.
func ValidateMemoryPath(root, rel string) (cleanedAbs string, err error)

// IsDerivedPath reports whether rel refers to a server-managed derived file
// (sqlite index + sidecars, advisory lock) that agents must never write
// through ValidateMemoryPath.
func IsDerivedPath(rel string) bool
```

### Validation algorithm

1. **`root` must be absolute.** Defensive guard against caller bugs.
2. **`rel` must be non-empty and relative.** Absolute `rel` paths are rejected outright.
3. **Normalize and clean.** `filepath.FromSlash` then `filepath.Clean`. This collapses redundant separators and embedded `.` segments, and resolves `..` segments where possible.
4. **Reject escapes.** After cleaning, if the path equals `..` or starts with `..` + separator, it escapes the root. Reject.
5. **Defensive second pass.** Even though `Clean` should have collapsed `..` segments, re-scan every segment for a literal `..`. Catches platform-specific quirks.
6. **Reject derived paths.** Via `IsDerivedPath` against the forward-slash form of the cleaned path.
7. **Join.** Return `filepath.Join(root, cleaned)`.

Symlink resolution is **intentionally not performed** in the validator. The engine's filesystem operations (`os.OpenFile`, `os.ReadFile`, atomic-write rename) follow symlinks transparently. If a project ever sees symlink-based exploits, the right fix is to add `filepath.EvalSymlinks` + a re-check at the validator layer; for now we defer.

## Derived path list

Refused targets:

| Path | Why refused |
|---|---|
| `meta/index.sqlite` (and `*-wal`, `*-shm`, `*-journal` sidecars) | Owned by the SQLite driver. Agent writes would corrupt the FTS5 index. |
| `meta/lock` | Owned by the advisory-lock subsystem. Agent writes would damage the JSON metadata or compete with the OS lock. |

Allowed (server-interpreted but stored as canonical text):

| Path | Why allowed |
|---|---|
| `meta/manifest.yaml`, `meta/schema.yaml` | Configuration. Agent writes flow through normal `propose_update` staging — never direct. |
| `index.md` | Server-maintained but agents may not write it directly; enforced at a higher layer (intent + category routing) rather than at the path validator. |

The rule of thumb: **"files that ARE the server's runtime state"** are refused. **"Files the server interprets but stores as Markdown/YAML"** pass the validator and are gated by higher-layer policy (schema validation, approval routing).

## API contract

```go
root := "/repo/.agent-memory"  // absolute

// Accepted:
ValidateMemoryPath(root, "modules/auth.md")       // → /repo/.agent-memory/modules/auth.md
ValidateMemoryPath(root, "decisions.md")          // → /repo/.agent-memory/decisions.md
ValidateMemoryPath(root, "archive/2026-05.md")    // → /repo/.agent-memory/archive/2026-05.md
ValidateMemoryPath(root, "local/current.main.md") // → /repo/.agent-memory/local/current.main.md
ValidateMemoryPath(root, "modules/../foo.md")     // → /repo/.agent-memory/foo.md (resolves cleanly)

// Rejected (returns error, no path):
ValidateMemoryPath(root, "")                          // empty
ValidateMemoryPath(root, "/etc/passwd")               // absolute
ValidateMemoryPath(root, "..")                        // direct escape
ValidateMemoryPath(root, "../etc/passwd")             // escape
ValidateMemoryPath(root, "modules/../../etc/passwd")  // escape via embedded ..
ValidateMemoryPath(root, "meta/index.sqlite")         // derived
ValidateMemoryPath(root, "meta/index.sqlite-wal")     // derived sidecar
ValidateMemoryPath(root, "meta/lock")                 // derived
```

## When to use

Every code path that accepts a path from agent input or external config:

- `memory.propose_update` operations (every operation's `path` field).
- `memory.fetch_context` if `scope` ever takes raw paths.
- `archive_section`, `remove_section` — for both the source `path` and the `archive_path`.
- `create_file` for new paths.
- Any future CLI subcommand that takes a memory-relative path argument.

The validator runs **before** any filesystem access, so even logging a rejected path never touches the underlying file.

## What this pattern does NOT do

- **It does not check whether the file exists.** That's a separate concern (operation-specific: `replace_section` expects existence, `create_file` expects absence).
- **It does not resolve symlinks.** Callers that need to harden against symlink escapes must layer `filepath.EvalSymlinks` + re-validation on top.
- **It does not check permissions or quotas.** Out of scope.
- **It does not validate Markdown structure.** That's the Markdown engine's job.

## References

- [Implementation Plan §5.2 T1.2](../../agent-memory-implementation-plan.md).
- [Design Doc v0.4.1 §15.2, §21.5](../../agent-memory-design-doc-v0.4.1.md).
- [OWASP Path Traversal](https://owasp.org/www-community/attacks/Path_Traversal) — the broader threat model.
