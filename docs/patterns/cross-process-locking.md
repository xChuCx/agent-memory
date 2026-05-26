# Pattern: Cross-Process Locking

**Status:** Implemented in [`internal/lock/lock.go`](../../internal/lock/lock.go). Spike-validated in S3; production API tested in `internal/lock/lock_test.go`.
**Owner:** `internal/lock/` (M1).
**Tracks design:** [Design Doc v0.4.1 §11](../../agent-memory-design-doc-v0.4.1.md).

## Problem

The agent-memory server is single-writer per host. Multiple agents (Claude Code, Cursor, ...) and the developer may all attempt to modify `.agent-memory/` files concurrently. We need:

1. **Mutual exclusion.** Only one writer at a time.
2. **Crash safety.** A writer that dies without cleanup must not leave the lock permanently held.
3. **Cross-platform.** Linux, Windows, macOS.
4. **No TTL bookkeeping.** The v0.4.1 design (§11) explicitly chose OS-level advisory locks over PID-file-with-TTL to avoid race-prone stale-recovery code.

## Solution

OS-level advisory file locks via `github.com/gofrs/flock`. On POSIX this calls `flock(2)`; on Windows it calls `LockFileEx`. In both cases the kernel owns lock state — when the holding process exits (clean exit, panic, OOM, SIGKILL, power loss), the kernel releases the lock automatically.

```go
fl := flock.New(".agent-memory/meta/lock")
if err := fl.Lock(); err != nil { /* ... */ }  // blocking
// ... critical section ...
fl.Unlock()
```

`TryLock` is the non-blocking variant. The production wrapper combines them:

1. `TryLock` first (most common case: lock is free).
2. If contended, block with a timeout (`concurrency.wait_timeout_seconds`, default 10s).
3. Return `ErrLockHeld` on timeout so callers can surface a friendly error.

## Properties relied on

- **Single-writer per host.** No multi-host coordination needed in v0.x (design doc §11.8).
- **Kernel-managed lock state.** Application code never inspects whether a lock is "stale". The kernel releases on process death; we don't retry or recover.
- **Informational metadata.** The lock file's *content* (`{owner_pid, owner_id, op_id, acquired_at}`) is purely for `status` debugging. It never gates correctness. Stale metadata after a crash is harmless — the next acquirer overwrites it.
- **Reader-free critical section.** Read paths (`fetch_context`, `status`) bypass the lock; atomic-rename writes (M1) ensure readers always see a consistent file state.

## Critical sections that take the lock

| Operation | Why |
|---|---|
| `propose_update` (apply path) | Mutates durable files + index. |
| `apply <staging_id>` | Same as above. |
| `archive_section`, `remove_section`, `rename_heading` | Mutates source file (and creates archive). |
| `rebuild-index` | Mutates the SQLite shadow index. |

| Operation | Why no lock |
|---|---|
| `fetch_context` | Read-only; atomic-rename writes mean readers see either pre- or post-write state. |
| `status` | Read-only; lock metadata read is best-effort. |
| `propose_update` (stage path) | Creates a timestamp-named staging directory; no contention possible. |

## Implementation API (`internal/lock/`)

```go
var ErrLockHeld = errors.New("lock held by another process")

type AcquireOpts struct {
    // 0 = TryLock once; >0 = poll until timeout.
    WaitTimeout time.Duration
    // Filled into the lock-file metadata on success. Empty fields default:
    // OwnerPID = os.Getpid(), AcquiredAt = time.Now().UTC().
    Owner Metadata
}

type Metadata struct {
    OwnerPID   int       `json:"owner_pid"`
    OwnerID    string    `json:"owner_id"`
    OwnerKind  string    `json:"owner_kind"`   // "agent" | "cli" | "cli-merge-driver" | ...
    AcquiredAt time.Time `json:"acquired_at"`
    OpID       string    `json:"op_id,omitempty"`
}

type Lock struct { /* ... */ }
func (l *Lock) Path() string { /* ... */ }

func Acquire(path string, opts AcquireOpts) (*Lock, error)
func (l *Lock) Release() error
func ReadMetadata(path string) (Metadata, error)
```

### Acquisition

1. `flock.New(path)` (does not yet open the file).
2. If `WaitTimeout > 0` → `TryLockContext(ctx, 10ms)` (blocking with poll); else `TryLock` (single attempt).
3. On context-deadline error → return `ErrLockHeld`. On other error → wrap and return.
4. On `locked == false` (deadline reached) → return `ErrLockHeld`.
5. On success: fill in metadata defaults, write JSON metadata through the *same file handle* that holds the lock (`fl.Fh().Truncate(0) → Seek(0,0) → Write(json)`). Best-effort; failure does not fail Acquire.

### Release

`Lock.Release()` calls `fl.Unlock()`, which closes the underlying file handle. The kernel releases the OS advisory lock atomically as part of close. The lock file persists on disk (next acquirer reuses it). `Release` is idempotent: a second call (or a call on a nil `*Lock`) returns nil.

### Reading metadata

`ReadMetadata(path)` reads the lock file *without* acquiring the lock and decodes its JSON contents. Returns an empty `Metadata` and no error for: missing file, empty file, or malformed JSON. The OS lock remains the ground truth for whether the lock is held — metadata is purely for `status` debugging.

### Why write metadata through the locked handle

Two reasons:

1. **Atomicity vs. competing writers.** While the lock is held, only one process can write to the file via its own handle. Opening a second file descriptor for writing would, on Windows, risk sharing-violation errors.
2. **No second open.** Re-opening the file path while flock is held has platform-dependent semantics on POSIX (per-FD locks) and Windows (per-handle byte-range locks). Using the existing handle sidesteps the issue entirely.

`gofrs/flock` opens the file with `O_CREATE|O_RDWR` and exposes the handle via `Fh()`, so write access is guaranteed.

## Alternatives considered

### PID file with TTL

Write `{pid, acquired_at, ttl_seconds}` to a lock file; subsequent acquirers compare timestamps and break stale locks. **Rejected** in v0.4.1 §0:

- Race conditions on the cleanup path (two acquirers see a stale lock simultaneously).
- Manual TTL tuning is bug-prone (too short → false breaks; too long → real outages).
- Clock-skew sensitivity.

### Database-backed lock (e.g., SQLite `BEGIN IMMEDIATE`)

**Rejected**: the SQLite shadow index is derived state; making it the lock substrate would couple unrelated concerns. The lock outlives any single SQLite operation.

### Distributed lock (Consul / etcd / Redis)

**Rejected** as out-of-scope: v0.x is single-host (§11.8).

## Validation

The two critical properties were validated end-to-end on Windows 10 in spike S3:

- **Cross-process serialization** — 10 subprocess workers, no overlap, 520ms total (~theoretical minimum).
- **Crash recovery** — second worker acquired the lock 4.6ms after the holder was killed and reaped (1000× under the 1s SLA).

See [s3-results.md](../spikes/s3-results.md) for the full validation record.

The production implementation in `internal/lock/lock.go` is exercised by `internal/lock/lock_test.go`:

| Test | What |
|---|---|
| `TestAcquireRelease` | Basic Acquire → Release happy path. |
| `TestAcquireReleaseReacquire` | Same path can be re-locked after release. |
| `TestReleaseIsIdempotent` | Second Release and nil-Lock Release are no-ops. |
| `TestMetadataRoundTrip` | Metadata written by Acquire is read back by ReadMetadata. |
| `TestReadMetadata_DefaultsAreFilled` | OwnerPID and AcquiredAt are populated even with zero-value Owner. |
| `TestReadMetadata_MissingFile` | Returns empty Metadata, no error. |
| `TestReadMetadata_MalformedFile` | Returns empty Metadata, no error. |
| `TestCrossProcessSerialization` | 5 subprocesses, no overlap (smoke test against the production API). |
| `TestCrashRecovery` | Holder Kill + Wait, contender acquires <1s later. |
| `TestAcquireTimeoutReturnsErrLockHeld` | Contender with WaitTimeout=100ms returns ErrLockHeld when holder holds. |

Subprocess tests use the same `TestMain` dispatch pattern as the S3 spike: the test binary, when invoked with `LOCK_TEST_WORKER=1` in the environment, runs `runLockWorker()` and exits instead of running tests. This sidesteps the platform-dependent semantics of within-process flock and exercises the property that actually matters in production (separate processes).

Cross-platform CI verification (Linux + macOS) runs via the M0 `.github/workflows/ci.yml` matrix on every push.

## References

- [Design Doc v0.4.1 §11](../../agent-memory-design-doc-v0.4.1.md) — concurrency and locking model.
- [Spike S3 Results](../spikes/s3-results.md) — empirical validation.
- [Implementation Plan §3 S3](../../agent-memory-implementation-plan.md).
- [gofrs/flock](https://github.com/gofrs/flock) — the SDK used.
