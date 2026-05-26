# Pattern: Cross-Process Locking

**Status:** Sketched. Pending empirical validation in spike S3.
**Owner:** `internal/lock/` (from M1).
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

## Implementation API (target: `internal/lock/`)

```go
type AcquireOpts struct {
    WaitTimeout time.Duration // 0 = TryLock only; >0 = blocking with timeout
    Owner       Metadata
}

type Metadata struct {
    OwnerPID   int
    OwnerID    string
    OwnerKind  string    // "agent" | "cli" | "cli-merge-driver" | ...
    AcquiredAt time.Time
    OpID       string
}

func Acquire(path string, opts AcquireOpts) (*Lock, error)
func (l *Lock) Release() error
func ReadMetadata(path string) (Metadata, error)
```

Acquisition:

1. `Acquire` opens the lock file (created if absent).
2. Attempts `TryLock`.
3. If failed and `WaitTimeout > 0`, blocks on `Lock` for up to `WaitTimeout`.
4. On success: truncates the file and writes the metadata JSON (best-effort, failure is non-fatal).
5. On timeout: returns `ErrLockHeld` with whatever metadata was readable from the file (informational only).

Release: `Lock.Release()` closes the file handle. The kernel releases the OS lock atomically as part of close. The file persists; only the lock is transient.

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

Spike S3 builds two tests that exercise the two critical properties (cross-process serialization and crash recovery) on the actual host platform. Results in [s3-results.md](../spikes/s3-results.md).

Cross-platform CI verification (Linux + macOS) is a follow-up before M1 lands the production implementation.

## References

- [Design Doc v0.4.1 §11](../../agent-memory-design-doc-v0.4.1.md) — concurrency and locking model.
- [Spike S3 Results](../spikes/s3-results.md) — empirical validation.
- [Implementation Plan §3 S3](../../agent-memory-implementation-plan.md).
- [gofrs/flock](https://github.com/gofrs/flock) — the SDK used.
