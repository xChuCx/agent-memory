// Package s3 verifies that github.com/gofrs/flock provides cross-process
// mutual exclusion and automatic release on process death.
//
// The package is used in two modes:
//
//   1. As a normal test package: `go test ./spikes/s3-flock-cross-process/...`
//      runs TestCrossProcessSerialization and TestCrashRecovery.
//
//   2. As a "worker": when the test binary is re-invoked by a parent test with
//      FLOCK_WORKER=1 in the environment, TestMain dispatches to RunWorker(),
//      which acquires the lock, writes start/end markers to a sentinel file,
//      and exits — never running any actual tests.
//
// See ../../docs/spikes/s3-results.md and ../../docs/patterns/cross-process-locking.md.
package s3

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gofrs/flock"
)

// Environment variables that drive the subprocess worker mode.
const (
	EnvWorker       = "FLOCK_WORKER"
	EnvLockPath     = "FLOCK_PATH"
	EnvSentinelPath = "FLOCK_SENTINEL"
	EnvWorkerID     = "FLOCK_WORKER_ID"
	EnvHoldMs       = "FLOCK_HOLD_MS"
	EnvHoldForever  = "FLOCK_HOLD_FOREVER"
)

// RunWorker is invoked by the test binary in subprocess mode. It blocks on
// the lock at FLOCK_PATH, writes "<id>:start <pid> <unixnano>" to the
// sentinel, optionally sleeps, writes "<id>:end <pid> <unixnano>", releases.
//
// Both sentinel writes happen while the lock is held, so concurrent workers
// cannot interleave their markers.
//
// Exit codes:
//   0 — success
//   2 — missing required env var
//   3 — flock.Lock failed
//   4 — sentinel write failed
//   5 — flock.Unlock failed
func RunWorker() int {
	lockPath := os.Getenv(EnvLockPath)
	sentinelPath := os.Getenv(EnvSentinelPath)
	id := os.Getenv(EnvWorkerID)

	if lockPath == "" || sentinelPath == "" || id == "" {
		log.Printf("worker: missing env: lock=%q sentinel=%q id=%q",
			lockPath, sentinelPath, id)
		return 2
	}

	fl := flock.New(lockPath)
	if err := fl.Lock(); err != nil {
		log.Printf("worker %s: Lock: %v", id, err)
		return 3
	}

	if err := appendLine(sentinelPath, fmt.Sprintf("%s:start %d %d\n",
		id, os.Getpid(), time.Now().UnixNano())); err != nil {
		log.Printf("worker %s: write start: %v", id, err)
		_ = fl.Unlock()
		return 4
	}

	switch {
	case os.Getenv(EnvHoldForever) != "":
		// Hold until killed by the parent test.
		time.Sleep(10 * time.Minute)
	default:
		if ms, err := strconv.Atoi(os.Getenv(EnvHoldMs)); err == nil && ms > 0 {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
	}

	if err := appendLine(sentinelPath, fmt.Sprintf("%s:end %d %d\n",
		id, os.Getpid(), time.Now().UnixNano())); err != nil {
		log.Printf("worker %s: write end: %v", id, err)
		_ = fl.Unlock()
		return 4
	}

	if err := fl.Unlock(); err != nil {
		log.Printf("worker %s: Unlock: %v", id, err)
		return 5
	}
	return 0
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}
