package s3

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain dispatches: if FLOCK_WORKER is set in the environment, run the
// worker logic and exit. Otherwise run the test suite normally.
//
// This makes the test binary re-usable as a subprocess: parent tests spawn
// the same binary with FLOCK_WORKER=1, which never gets past TestMain into
// the test framework.
func TestMain(m *testing.M) {
	if os.Getenv(EnvWorker) != "" {
		os.Exit(RunWorker())
	}
	os.Exit(m.Run())
}

// TestCrossProcessSerialization spawns N subprocess workers that each acquire
// the same lock, write start/end timestamps to a shared sentinel, hold for a
// short duration, and release. After all workers exit, the test parses the
// sentinel and asserts no two workers' [start, end) intervals overlap.
func TestCrossProcessSerialization(t *testing.T) {
	const numWorkers = 10
	const holdMs = 50

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatalf("create sentinel: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(),
				EnvWorker+"=1",
				EnvLockPath+"="+lockPath,
				EnvSentinelPath+"="+sentinel,
				EnvWorkerID+"="+strconv.Itoa(id),
				EnvHoldMs+"="+strconv.Itoa(holdMs),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs <- fmt.Errorf("worker %d: %v\noutput:\n%s", id, err, out)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("%v", err)
	}

	intervals, err := parseSentinel(sentinel)
	if err != nil {
		t.Fatalf("parseSentinel: %v", err)
	}
	if len(intervals) != numWorkers {
		t.Fatalf("expected %d worker intervals, got %d:\n%s",
			numWorkers, len(intervals), formatIntervals(intervals))
	}
	if err := assertNoOverlap(intervals); err != nil {
		t.Errorf("overlap detected: %v\nall intervals:\n%s",
			err, formatIntervals(intervals))
	}
}

// TestCrashRecovery spawns one subprocess that acquires the lock and hangs.
// We kill it via os.Process.Kill (SIGKILL on POSIX, TerminateProcess on
// Windows). Then we spawn a second worker and assert it acquires the lock
// within 1 second of the first process being reaped.
//
// This validates the central property of OS-level advisory locks: the kernel
// releases on process death, without any application-level recovery code.
func TestCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatalf("create sentinel: %v", err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// Spawn the holder.
	holder := exec.Command(exe)
	holder.Env = append(os.Environ(),
		EnvWorker+"=1",
		EnvLockPath+"="+lockPath,
		EnvSentinelPath+"="+sentinel,
		EnvWorkerID+"=1",
		EnvHoldForever+"=1",
	)
	if err := holder.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		// Defensive: if the test fails partway, make sure we don't leak.
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	})

	// Poll for "1:start" in the sentinel to confirm the holder has the lock.
	deadline := time.Now().Add(5 * time.Second)
	acquired := false
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(sentinel); err == nil && strings.Contains(string(b), "1:start") {
			acquired = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !acquired {
		b, _ := os.ReadFile(sentinel)
		t.Fatalf("holder never acquired the lock within 5s\nsentinel:\n%s", b)
	}

	// Kill the holder and wait for the kernel to reap it. After Wait returns,
	// the OS has released the lock.
	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_, _ = holder.Process.Wait()
	killReapedAt := time.Now()

	// Spawn the second worker. It should acquire the lock immediately because
	// the kernel released it when the holder died.
	second := exec.Command(exe)
	second.Env = append(os.Environ(),
		EnvWorker+"=1",
		EnvLockPath+"="+lockPath,
		EnvSentinelPath+"="+sentinel,
		EnvWorkerID+"=2",
		EnvHoldMs+"=10",
	)
	if out, err := second.CombinedOutput(); err != nil {
		t.Fatalf("second worker: %v\noutput:\n%s", err, out)
	}

	intervals, err := parseSentinel(sentinel)
	if err != nil {
		t.Fatalf("parseSentinel: %v", err)
	}

	var secondStart int64
	for _, iv := range intervals {
		if iv.ID == 2 {
			secondStart = iv.Start
			break
		}
	}
	if secondStart == 0 {
		t.Fatalf("second worker never recorded start; intervals:\n%s",
			formatIntervals(intervals))
	}

	acquireDelay := time.Duration(secondStart - killReapedAt.UnixNano())
	t.Logf("acquire delay after crash: %v", acquireDelay)
	if acquireDelay > time.Second {
		t.Errorf("second worker acquired %v after kill (>1s)", acquireDelay)
	}
}

// ---------- helpers ----------

type Interval struct {
	ID    int
	Start int64 // unix nano
	End   int64 // unix nano; 0 if worker did not write an end marker (e.g., killed mid-hold)
}

func parseSentinel(path string) ([]Interval, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	starts := make(map[int]int64)
	ends := make(map[int]int64)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		head := strings.SplitN(parts[0], ":", 2)
		if len(head) != 2 {
			continue
		}
		id, err := strconv.Atoi(head[0])
		if err != nil {
			continue
		}
		ts, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		switch head[1] {
		case "start":
			starts[id] = ts
		case "end":
			ends[id] = ts
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	intervals := make([]Interval, 0, len(starts))
	for id, s := range starts {
		intervals = append(intervals, Interval{
			ID:    id,
			Start: s,
			End:   ends[id], // 0 if missing
		})
	}
	return intervals, nil
}

// assertNoOverlap ignores intervals that lack an End (crashed workers).
func assertNoOverlap(intervals []Interval) error {
	sorted := make([]Interval, 0, len(intervals))
	for _, iv := range intervals {
		if iv.End > 0 {
			sorted = append(sorted, iv)
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start < sorted[j].Start
	})
	for i := 1; i < len(sorted); i++ {
		prev := sorted[i-1]
		cur := sorted[i]
		if cur.Start < prev.End {
			return fmt.Errorf(
				"worker %d [%d, %d) overlaps with worker %d [%d, %d)",
				prev.ID, prev.Start, prev.End,
				cur.ID, cur.Start, cur.End)
		}
	}
	return nil
}

func formatIntervals(intervals []Interval) string {
	cp := make([]Interval, len(intervals))
	copy(cp, intervals)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })

	var b strings.Builder
	for _, iv := range cp {
		if iv.End == 0 {
			fmt.Fprintf(&b, "  worker %d: start=%d end=<missing>\n", iv.ID, iv.Start)
		} else {
			fmt.Fprintf(&b, "  worker %d: start=%d end=%d dur=%v\n",
				iv.ID, iv.Start, iv.End, time.Duration(iv.End-iv.Start))
		}
	}
	return b.String()
}
