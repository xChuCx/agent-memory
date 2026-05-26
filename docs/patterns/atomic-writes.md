# Pattern: Atomic Writes

**Status:** Implemented in [`internal/fs/atomic.go`](../../internal/fs/atomic.go).
**Owner:** `internal/fs/` (M1).
**Tracks design:** [Design Doc v0.4.1 §15.2](../../agent-memory-design-doc-v0.4.1.md) (pipeline step 8), §21.

## Problem

When the server writes a memory file, a concurrent reader (another agent process, an editor that's watching the file, a future `fetch_context` call) must always observe either the **old complete contents** or the **new complete contents**. A partial write — old and new bytes mixed — would produce visible corruption:

- `git blame` sees an intermediate state that never semantically existed.
- A Markdown parser gets a malformed AST and rejects the file.
- A backup or `rebuild-index` taken at the wrong instant captures an inconsistent snapshot.

We need writes to be **atomic at the filesystem level**.

## Solution

The canonical write-temp-then-rename pattern:

1. **Open a temp file in the same directory as the target.** Cross-device renames are not atomic on POSIX; same-directory rename always is on both POSIX and NTFS.
2. **Write all data, then fsync the temp file.** Ensures the bytes are on durable storage before the rename publishes them.
3. **Close the temp file.**
4. **Rename to the target path.** This is the atomic step. Both POSIX `rename(2)` and NTFS `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING` guarantee atomicity within a single filesystem.
5. **On POSIX, fsync the containing directory.** Makes the rename durable across power loss. Windows skips this step: NTFS handles rename durability internally, and `(*os.File).Sync` on a directory handle is not meaningful there.

If any step fails, the temp file is removed; the original target (if any) is left untouched.

## Properties

| Property | Guarantee |
|---|---|
| **Atomicity** | Readers see either the pre-write or post-write state, never a mixed state. |
| **No tear under concurrency** | Multiple writers to the same path produce last-writer-wins; every observable state is some writer's complete output. Verified by `TestWriteAtomic_ConcurrentNoTear` (50 goroutines × 1024 bytes; final file always contains a single intact write). |
| **Cleanup on failure** | No `.tmp.XXXXXXXX` leaks if the write fails midway. Verified by `TestWriteAtomic_NoTempLeak` and the concurrent test. |
| **Durability (POSIX)** | After successful return, the rename survives power loss. |
| **Durability (Windows)** | NTFS's rename transactional semantics; no explicit directory fsync. |

## API

```go
// WriteAtomic writes data to path atomically. Returns an error if path is
// not absolute, the parent directory does not exist, or any I/O step fails.
// On error, no temp files are left in the parent directory.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error

// PathExists is a small convenience: reports whether path exists, treating
// "does not exist" as false and any other error as true (defensive).
func PathExists(path string) bool
```

## Trade-offs and constraints

- **In-memory data.** The API takes `[]byte`, not `io.Reader`. For our memory files (typically <100KB each) this is the right shape; a streaming variant would be useful for hypothetical large blobs but is out of scope.
- **Permission propagation.** The temp file is opened with the requested `perm` via `os.OpenFile`, so umask interference is avoided. The final file has the same `perm`.
- **No symlink hardening.** `WriteAtomic` does not check whether `path` traverses a symlink. Callers that accept paths from untrusted sources should run them through [`ValidateMemoryPath`](path-validation.md) first.
- **Windows directory fsync skipped.** Acceptable because NTFS already provides the durability we need; testing on Windows would require a power-fail simulation that's outside our test infrastructure.

## When to use

Whenever the server writes a file that another process may read:

- Markdown files in `.agent-memory/` (durable, local, archive).
- `meta/manifest.yaml`, `meta/schema.yaml`.
- Staging proposals (`staging/<id>/proposal.json`, `staging/<id>/preview.diff`, `staging/<id>/target-checksums.json`).
- The server-managed `index.md`.

Do NOT use for:

- The SQLite shadow index (`meta/index.sqlite`) — SQLite manages its own atomic writes via WAL.
- The lock file (`meta/lock`) — written through the locked file handle, not a separate rename.
- Append-only logs (sessions) — append semantics, not full-file replace.

## References

- [Implementation Plan §5.2 T1.1](../../agent-memory-implementation-plan.md).
- [Design Doc v0.4.1 §15.2, §21.7](../../agent-memory-design-doc-v0.4.1.md).
- [LWN — When write() doesn't write](https://lwn.net/Articles/322823/) (POSIX rename atomicity).
- [Microsoft Docs — MoveFileEx](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexa) (Windows replace semantics).
