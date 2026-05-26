# Spike S3 — gofrs/flock Cross-Process Verification

**Status:** Validated. Decision: **GO**.
**Started:** 2026-05-26
**Closed:** 2026-05-26
**Goal:** Verify OS-level advisory file locks via `github.com/gofrs/flock` provide cross-process mutual exclusion and automatic release on process death.

## Decision: GO

Both properties hold cleanly on Windows. Linux + macOS verification follows via CI before M1 lands, but cross-platform parity is highly likely — `gofrs/flock` is a thin wrapper over `flock(2)` (POSIX) and `LockFileEx` (Windows), both kernel-managed. Approach approved for `internal/lock/` in M1.

## How to validate

```powershell
go mod tidy
go test -v ./spikes/s3-flock-cross-process/...
```

Expected output (paraphrased):

```
=== RUN   TestCrossProcessSerialization
--- PASS: TestCrossProcessSerialization
=== RUN   TestCrashRecovery
    flock_test.go: acquire delay after crash: <Xms>
--- PASS: TestCrashRecovery
PASS
ok      .../s3-flock-cross-process    <Ys>
```

Wallclock: TestCrossProcessSerialization is bounded by 10 workers × 50ms = ~500ms minimum, plus subprocess startup overhead. TestCrashRecovery should complete in a few hundred ms.

## Method

See [spikes/s3-flock-cross-process/README.md](../../spikes/s3-flock-cross-process/README.md). Two tests:

1. **TestCrossProcessSerialization** — 10 subprocesses contend for the same lock; each holds for 50ms; the test asserts non-overlapping `[start, end)` intervals across all workers.
2. **TestCrashRecovery** — one subprocess acquires the lock and sleeps indefinitely; we `Kill` + `Wait` it; a second subprocess must acquire within 1 second of the kernel reaping the first.

Subprocesses are real processes spawned via `os/exec`, not goroutines: within-process flock semantics are platform-dependent. The test binary doubles as the worker via a `TestMain` dispatch on `FLOCK_WORKER=1`.

## Findings (running notes)

### 2026-05-26 — Initial implementation

Code:

- `spikes/s3-flock-cross-process/worker.go` (~75 lines).
- `spikes/s3-flock-cross-process/flock_test.go` (~200 lines).

Approach choices, captured in [cross-process-locking.md](../patterns/cross-process-locking.md):

- OS advisory locks only; no PID file with TTL.
- Subprocess-based testing (not goroutines).
- `TestMain` dispatch via env var (`FLOCK_WORKER=1`).
- Sentinel writes happen inside the critical section, so concurrent appends cannot interleave.
- Kernel-managed release on process death — no application recovery logic.

go.mod dependency added: `github.com/gofrs/flock` (version pinned by `go mod tidy` to the latest stable release).

### 2026-05-26 — Validated on Windows 10 + Go 1.22+

Resolved gofrs/flock version: **v0.12.1** (from go.mod after `go mod tidy`).

`go test -v ./spikes/s3-flock-cross-process/...` output:

```
=== RUN   TestCrossProcessSerialization
--- PASS: TestCrossProcessSerialization (0.52s)
=== RUN   TestCrashRecovery
    flock_test.go:185: acquire delay after crash: 4.6333ms
--- PASS: TestCrashRecovery (0.04s)
PASS
ok      github.com/agent-memory/agent-memory/spikes/s3-flock-cross-process      0.577s
```

| Check | Result | Notes |
|---|---|---|
| 10 cross-process workers serialize correctly | PASS | No overlap detected. Total wallclock 520ms ≈ theoretical lower bound (10 × 50ms hold). Subprocess startup overhead is the ~20ms slack. |
| Crash recovery releases lock automatically | PASS | Second worker acquired **4.6ms** after `Kill` + `Wait` reaped the holder. Two orders of magnitude under the 1-second SLA in the plan. |
| `TerminateProcess` releases `LockFileEx` lock | PASS | Confirmed indirectly — second worker acquired without any application-level recovery code. |
| `os.Executable()` + env-var dispatch pattern | PASS | `TestMain` `FLOCK_WORKER=1` dispatch works as designed; subprocess never enters test framework. |

No platform quirks observed on Windows. The design's no-TTL, no-stale-recovery posture is justified — the kernel handles everything.

## Decision outcome

**GO.** OS-level advisory locks via `gofrs/flock` are validated for the design's concurrency model. M1 `internal/lock/` adopts this pattern directly. Cross-platform CI runs on Linux + macOS scheduled as part of M0 (`.github/workflows/ci.yml`).

## Next steps after GO

1. Move the spike pattern into `internal/lock/` during M1.
2. Add the `Metadata` JSON wrapper (lock file content for `status` debugging).
3. Wire the timeout-and-retry policy from `manifest.yaml` `concurrency.wait_timeout_seconds`.
4. Reuse the subprocess-based test pattern in `internal/lock/lock_test.go`.
5. Schedule Linux + macOS CI runs of the same tests as part of M0 (`.github/workflows/ci.yml`).

## Next steps if NO-GO

1. Identify which property failed (mutual exclusion or crash release).
2. Investigate platform-specific workarounds (e.g., gofrs/flock has `LockContext` and other variants).
3. If a platform fundamentally cannot satisfy the property, document the constraint and re-evaluate the v0.4.1 §11 concurrency design (worst case: fall back to PID-file with careful TTL, accepting the complexity cost flagged in v0.4.1 §0).
