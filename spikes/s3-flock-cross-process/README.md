# Spike S3 — gofrs/flock Cross-Process Verification

**Purpose:** Verify that OS-level advisory file locks via `github.com/gofrs/flock` give us the two concurrency properties the agent-memory design depends on:

1. **Cross-process mutual exclusion.** When N processes contend for the same lock, their critical sections do not overlap.
2. **Automatic release on process death.** When a lock-holding process is killed (SIGKILL / TerminateProcess), the kernel releases the lock and the next acquirer succeeds promptly.

If both properties hold, our `internal/lock/` implementation in M1 collapses to ~30 lines around `flock.New + Lock + Unlock` — no TTL bookkeeping, no stale-recovery code, no race-prone cleanup paths. That's the v0.4.1 design (§11).

## How to run

```powershell
go mod tidy
go test -v ./spikes/s3-flock-cross-process/...
```

Cross-platform check: run the same command on Linux and macOS (CI matrix or alternate machine).

Expected:

```
=== RUN   TestCrossProcessSerialization
--- PASS: TestCrossProcessSerialization
=== RUN   TestCrashRecovery
    flock_test.go:177: acquire delay after crash: <under 1s>
--- PASS: TestCrashRecovery
PASS
```

## What the tests do

### TestCrossProcessSerialization

1. Spawn 10 subprocesses of the same test binary.
2. Each subprocess (in worker mode, dispatched via `FLOCK_WORKER` env):
   - `flock.New(path).Lock()` (blocking).
   - Write `<id>:start <pid> <unixnano>` to a shared sentinel.
   - Sleep 50ms.
   - Write `<id>:end <pid> <unixnano>`.
   - `Unlock`.
3. Parent parses the sentinel and asserts no two `[start, end)` intervals overlap.

The sentinel writes happen *inside* the critical section, so concurrent writers cannot interleave markers — the lock itself is what we are testing.

### TestCrashRecovery

1. Spawn a "holder" subprocess that acquires the lock and sleeps 10 minutes.
2. Poll the sentinel for `1:start` to confirm the holder has the lock.
3. `process.Kill()` and `Wait()` to reap. After Wait returns, the kernel has released the lock.
4. Spawn a second worker. It should acquire promptly.
5. Assert the second worker's `2:start` timestamp is within 1 second of the kernel-reap moment.

## Why subprocesses, not goroutines

Within-process `flock(2)` semantics on POSIX are undefined when multiple file descriptors of the same file are involved. On Windows, `LockFileEx` is per-handle and reentrancy is not guaranteed. The property we actually care about — that *separate processes* serialize correctly — requires real subprocesses.

The test binary doubles as the worker: parent tests spawn `os.Executable()` with `FLOCK_WORKER=1`, and `TestMain` dispatches to `RunWorker()` and exits before any test framework code runs.

## Files

- `worker.go` — `RunWorker()` invoked from `TestMain` when `FLOCK_WORKER` is set.
- `flock_test.go` — `TestMain` dispatch, two tests, sentinel parser, overlap detector.

## See also

- [Pattern: Cross-Process Locking](../../docs/patterns/cross-process-locking.md)
- [Spike S3 Results](../../docs/spikes/s3-results.md)
- [Design Doc v0.4.1 §11](../../agent-memory-design-doc-v0.4.1.md)
- [Implementation Plan §3 S3](../../agent-memory-implementation-plan.md)
- [gofrs/flock on GitHub](https://github.com/gofrs/flock)
