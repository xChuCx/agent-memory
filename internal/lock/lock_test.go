package lock

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

// Subprocess-worker dispatch environment variables.
// When LOCK_TEST_WORKER is set in the env, TestMain invokes a worker that
// re-uses the test binary as the subprocess executable. The worker pattern
// mirrors spike S3 but exercises the production Acquire/Release API.
const (
	envWorker    = "LOCK_TEST_WORKER"
	envPath      = "LOCK_TEST_PATH"
	envSentinel  = "LOCK_TEST_SENTINEL"
	envWorkerID  = "LOCK_TEST_WORKER_ID"
	envHoldMs    = "LOCK_TEST_HOLD_MS"
	envForever   = "LOCK_TEST_FOREVER"
	envWaitMs    = "LOCK_TEST_WAIT_MS"
	envExpectErr = "LOCK_TEST_EXPECT_ERR"
)

func TestMain(m *testing.M) {
	if os.Getenv(envWorker) != "" {
		os.Exit(runLockWorker())
	}
	os.Exit(m.Run())
}

func runLockWorker() int {
	path := os.Getenv(envPath)
	sentinel := os.Getenv(envSentinel)
	id := os.Getenv(envWorkerID)
	holdMs, _ := strconv.Atoi(os.Getenv(envHoldMs))
	waitMs, _ := strconv.Atoi(os.Getenv(envWaitMs))
	forever := os.Getenv(envForever) != ""
	expectErr := os.Getenv(envExpectErr) != ""

	opts := AcquireOpts{
		Owner: Metadata{
			OwnerID:   id,
			OwnerKind: "test-worker",
			OpID:      "test-op-" + id,
		},
	}
	if waitMs > 0 {
		opts.WaitTimeout = time.Duration(waitMs) * time.Millisecond
	} else if !expectErr {
		// Default: be patient enough for the cross-process test under load.
		opts.WaitTimeout = 30 * time.Second
	}

	l, err := Acquire(path, opts)
	if err != nil {
		if expectErr && err == ErrLockHeld {
			return 0 // success: caller expected ErrLockHeld
		}
		fmt.Fprintf(os.Stderr, "worker %s: Acquire: %v\n", id, err)
		return 3
	}
	if expectErr {
		// Caller expected ErrLockHeld but we got the lock.
		_ = l.Release()
		fmt.Fprintf(os.Stderr, "worker %s: expected ErrLockHeld but acquired lock\n", id)
		return 4
	}

	appendLine(sentinel, fmt.Sprintf("%s:start %d %d\n", id, os.Getpid(), time.Now().UnixNano()))

	switch {
	case forever:
		time.Sleep(10 * time.Minute)
	case holdMs > 0:
		time.Sleep(time.Duration(holdMs) * time.Millisecond)
	}

	appendLine(sentinel, fmt.Sprintf("%s:end %d %d\n", id, os.Getpid(), time.Now().UnixNano()))
	_ = l.Release()
	return 0
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// ---------- in-process API tests ----------

func TestAcquireRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l, err := Acquire(path, AcquireOpts{
		Owner: Metadata{OwnerID: "x", OwnerKind: "cli"},
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l == nil {
		t.Fatal("Lock is nil after successful Acquire")
	}
	if l.Path() != path {
		t.Errorf("Path = %q, want %q", l.Path(), path)
	}
	if err := l.Release(); err != nil {
		t.Errorf("Release: %v", err)
	}
}

func TestAcquireReleaseReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	for i := 0; i < 3; i++ {
		l, err := Acquire(path, AcquireOpts{Owner: Metadata{OwnerID: fmt.Sprintf("iter-%d", i)}})
		if err != nil {
			t.Fatalf("iter %d: Acquire: %v", i, err)
		}
		if err := l.Release(); err != nil {
			t.Fatalf("iter %d: Release: %v", i, err)
		}
	}
}

func TestReleaseIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l, err := Acquire(path, AcquireOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Errorf("second Release should be a no-op: %v", err)
	}
	// And on a nil Lock.
	var nilLock *Lock
	if err := nilLock.Release(); err != nil {
		t.Errorf("nil Release should be a no-op: %v", err)
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	want := Metadata{
		OwnerPID:  12345,
		OwnerID:   "test-owner-xyz",
		OwnerKind: "agent",
		OpID:      "op-abc-123",
	}
	l, err := Acquire(path, AcquireOpts{Owner: want})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.OwnerPID != want.OwnerPID {
		t.Errorf("OwnerPID: got %d, want %d", got.OwnerPID, want.OwnerPID)
	}
	if got.OwnerID != want.OwnerID {
		t.Errorf("OwnerID: got %q, want %q", got.OwnerID, want.OwnerID)
	}
	if got.OwnerKind != want.OwnerKind {
		t.Errorf("OwnerKind: got %q, want %q", got.OwnerKind, want.OwnerKind)
	}
	if got.OpID != want.OpID {
		t.Errorf("OpID: got %q, want %q", got.OpID, want.OpID)
	}
	if got.AcquiredAt.IsZero() {
		t.Error("AcquiredAt is zero (should have been filled in by Acquire)")
	}
}

func TestReadMetadata_DefaultsAreFilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	l, err := Acquire(path, AcquireOpts{
		Owner: Metadata{}, // empty
	})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	got, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerPID != os.Getpid() {
		t.Errorf("OwnerPID: got %d, want %d (current pid)", got.OwnerPID, os.Getpid())
	}
	if got.AcquiredAt.IsZero() {
		t.Error("AcquiredAt was not filled in")
	}
}

func TestReadMetadata_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent-lock")
	got, err := ReadMetadata(path)
	if err != nil {
		t.Errorf("ReadMetadata on missing file should return no error: %v", err)
	}
	if got != (Metadata{}) {
		t.Errorf("expected empty Metadata for missing file, got %+v", got)
	}
}

func TestReadMetadata_MalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	// Metadata lives in the sidecar file (path + ".info"). Write garbage
	// there to verify ReadMetadata tolerates a malformed payload.
	if err := os.WriteFile(MetadataPath(path), []byte("not json {{{"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMetadata(path)
	if err != nil {
		t.Errorf("ReadMetadata on malformed file should return no error: %v", err)
	}
	if got != (Metadata{}) {
		t.Errorf("expected empty Metadata for malformed file, got %+v", got)
	}
}

func TestMetadataPath(t *testing.T) {
	got := MetadataPath("/foo/bar/lock")
	want := "/foo/bar/lock.info"
	if got != want {
		t.Errorf("MetadataPath = %q, want %q", got, want)
	}
}

// ---------- cross-process tests ----------

// TestCrossProcessSerialization is a smoke test that confirms our Lock
// wrapper preserves the cross-process serialization property already
// validated in spike S3.
func TestCrossProcessSerialization(t *testing.T) {
	const numWorkers = 5
	const holdMs = 30

	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatal(err)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, numWorkers)
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(),
				envWorker+"=1",
				envPath+"="+lockPath,
				envSentinel+"="+sentinel,
				envWorkerID+"="+strconv.Itoa(id),
				envHoldMs+"="+strconv.Itoa(holdMs),
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

	intervals := parseSentinel(t, sentinel)
	if len(intervals) != numWorkers {
		t.Fatalf("expected %d intervals, got %d", numWorkers, len(intervals))
	}
	assertNoOverlap(t, intervals)
}

// TestCrashRecovery: a holder subprocess acquires the lock and hangs; we
// Kill it; a second worker must acquire within 1 second of the kernel
// reaping the holder. Same property as S3 spike, against the production API.
func TestCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	holder := exec.Command(exe)
	holder.Env = append(os.Environ(),
		envWorker+"=1",
		envPath+"="+lockPath,
		envSentinel+"="+sentinel,
		envWorkerID+"=1",
		envForever+"=1",
	)
	if err := holder.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	})

	// Wait for the holder to mark "1:start" in the sentinel.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(sentinel)
		if strings.Contains(string(b), "1:start") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, _ := os.ReadFile(sentinel); !strings.Contains(string(b), "1:start") {
		t.Fatalf("holder never acquired the lock\nsentinel:\n%s", b)
	}

	if err := holder.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_, _ = holder.Process.Wait()
	killReapedAt := time.Now()

	second := exec.Command(exe)
	second.Env = append(os.Environ(),
		envWorker+"=1",
		envPath+"="+lockPath,
		envSentinel+"="+sentinel,
		envWorkerID+"=2",
		envHoldMs+"=10",
	)
	if out, err := second.CombinedOutput(); err != nil {
		t.Fatalf("second worker: %v\noutput:\n%s", err, out)
	}

	intervals := parseSentinel(t, sentinel)
	var secondStart int64
	for _, iv := range intervals {
		if iv.id == 2 {
			secondStart = iv.start
			break
		}
	}
	if secondStart == 0 {
		t.Fatalf("second worker never recorded start\nintervals: %+v", intervals)
	}
	acquireDelay := time.Duration(secondStart - killReapedAt.UnixNano())
	t.Logf("acquire delay after crash: %v", acquireDelay)
	if acquireDelay > time.Second {
		t.Errorf("second worker acquired %v after kill (>1s)", acquireDelay)
	}
}

// TestAcquireTimeoutReturnsErrLockHeld: a holder subprocess holds the lock;
// a contender subprocess attempts to acquire with WaitTimeout=100ms and
// expects ErrLockHeld.
func TestAcquireTimeoutReturnsErrLockHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, nil, 0644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	holder := exec.Command(exe)
	holder.Env = append(os.Environ(),
		envWorker+"=1",
		envPath+"="+lockPath,
		envSentinel+"="+sentinel,
		envWorkerID+"=1",
		envForever+"=1",
	)
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	})

	// Wait for holder to acquire.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(sentinel)
		if strings.Contains(string(b), "1:start") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Contender expects ErrLockHeld after a short wait.
	contender := exec.Command(exe)
	contender.Env = append(os.Environ(),
		envWorker+"=1",
		envPath+"="+lockPath,
		envSentinel+"="+sentinel,
		envWorkerID+"=2",
		envWaitMs+"=100",
		envExpectErr+"=1",
	)
	start := time.Now()
	if out, err := contender.CombinedOutput(); err != nil {
		t.Fatalf("contender: %v\noutput:\n%s", err, out)
	}
	elapsed := time.Since(start)
	t.Logf("contender wait+exit took %v", elapsed)
	if elapsed < 100*time.Millisecond {
		t.Errorf("contender returned too fast (%v); should have waited at least 100ms", elapsed)
	}
}

// ---------- sentinel helpers (shared with subprocess tests) ----------

type interval struct {
	id    int
	start int64
	end   int64
}

func parseSentinel(t *testing.T, path string) []interval {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open sentinel: %v", err)
	}
	defer f.Close()

	starts := map[int]int64{}
	ends := map[int]int64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) < 3 {
			continue
		}
		head := strings.SplitN(parts[0], ":", 2)
		if len(head) != 2 {
			continue
		}
		id, _ := strconv.Atoi(head[0])
		ts, _ := strconv.ParseInt(parts[2], 10, 64)
		switch head[1] {
		case "start":
			starts[id] = ts
		case "end":
			ends[id] = ts
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}

	out := make([]interval, 0, len(starts))
	for id, s := range starts {
		out = append(out, interval{id: id, start: s, end: ends[id]})
	}
	return out
}

func assertNoOverlap(t *testing.T, intervals []interval) {
	t.Helper()
	closed := make([]interval, 0, len(intervals))
	for _, iv := range intervals {
		if iv.end > 0 {
			closed = append(closed, iv)
		}
	}
	sort.Slice(closed, func(i, j int) bool { return closed[i].start < closed[j].start })
	for i := 1; i < len(closed); i++ {
		prev, cur := closed[i-1], closed[i]
		if cur.start < prev.end {
			t.Errorf("worker %d [%d, %d) overlaps with worker %d [%d, %d)",
				prev.id, prev.start, prev.end,
				cur.id, cur.start, cur.end)
		}
	}
}
