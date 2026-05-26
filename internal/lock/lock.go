// Package lock provides cross-process advisory locking around
// .agent-memory/meta/lock via github.com/gofrs/flock. The kernel owns lock
// state, so process death releases the lock automatically — no application
// stale-recovery code is needed.
//
// See docs/patterns/cross-process-locking.md for the design rationale and
// docs/spikes/s3-results.md for the empirical validation that motivated
// this approach.
package lock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gofrs/flock"
)

// ErrLockHeld is returned by Acquire when the lock cannot be obtained within
// AcquireOpts.WaitTimeout (or immediately, if WaitTimeout is zero).
var ErrLockHeld = errors.New("lock held by another process")

// metadataSuffix is appended to the lock file path to form the sidecar
// metadata file. The lock file itself stays empty so that gofrs/flock owns
// the OS-level lock without contention from our own writes; metadata lives
// in a separate readable JSON file.
//
// Rationale: gofrs/flock v0.12+ does not expose the underlying *os.File. On
// Windows, LockFileEx locks a byte range and prevents writes through any
// other handle; so we cannot open a second handle to the locked file for
// metadata. The sidecar avoids the constraint entirely.
const metadataSuffix = ".info"

// Metadata is the informational JSON written inside the lock file by the
// current holder. It NEVER gates correctness — the OS lock is ground truth.
// Stale metadata after a crashed holder is harmless: the next acquirer
// overwrites it.
type Metadata struct {
	OwnerPID   int       `json:"owner_pid"`
	OwnerID    string    `json:"owner_id"`             // e.g. "claude-code-session-abc"
	OwnerKind  string    `json:"owner_kind"`           // "agent" | "cli" | "cli-merge-driver" | ...
	AcquiredAt time.Time `json:"acquired_at"`
	OpID       string    `json:"op_id,omitempty"`
}

// AcquireOpts configures Acquire.
type AcquireOpts struct {
	// WaitTimeout caps how long Acquire blocks waiting for a contended lock:
	//   0  → TryLock once and return ErrLockHeld immediately if unavailable.
	//   >0 → TryLock then poll (every ~10ms) until the lock is available or
	//        the timeout expires.
	WaitTimeout time.Duration

	// Owner is the metadata to write into the lock file on successful
	// acquisition. Empty fields are filled in: OwnerPID defaults to
	// os.Getpid(), AcquiredAt defaults to time.Now().UTC().
	Owner Metadata
}

// Lock is a held advisory lock. Release it via Release(); the OS releases the
// underlying lock automatically on process exit even if Release is not called.
type Lock struct {
	fl   *flock.Flock
	path string
}

// Path returns the lock file path this Lock was acquired against.
func (l *Lock) Path() string { return l.path }

// Acquire opens (creating if missing) the file at path and tries to acquire
// an exclusive OS advisory lock on it.
//
// If the lock is held by another process and AcquireOpts.WaitTimeout is zero,
// Acquire returns ErrLockHeld immediately. If WaitTimeout is positive,
// Acquire polls until the lock is available or the timeout expires.
//
// On success, Acquire writes the owner metadata into the lock file. This is
// best-effort and never affects correctness.
//
// The lock file persists on disk; only the OS lock is transient. Callers
// should typically point path at .agent-memory/meta/lock.
func Acquire(path string, opts AcquireOpts) (*Lock, error) {
	fl := flock.New(path)

	var locked bool
	var err error
	if opts.WaitTimeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), opts.WaitTimeout)
		defer cancel()
		locked, err = fl.TryLockContext(ctx, 10*time.Millisecond)
	} else {
		locked, err = fl.TryLock()
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return nil, ErrLockHeld
	case err != nil:
		return nil, fmt.Errorf("lock: acquire %q: %w", path, err)
	case !locked:
		return nil, ErrLockHeld
	}

	owner := opts.Owner
	if owner.OwnerPID == 0 {
		owner.OwnerPID = os.Getpid()
	}
	if owner.AcquiredAt.IsZero() {
		owner.AcquiredAt = time.Now().UTC()
	}
	// Best-effort: metadata write failure does NOT fail Acquire. The lock is
	// already held; metadata is debugging info only.
	_ = writeMetadata(path, owner)

	return &Lock{fl: fl, path: path}, nil
}

// Release closes the lock file. The kernel releases the OS lock atomically
// as part of close. The lock file itself persists.
//
// Calling Release on an already-released Lock is a no-op.
func (l *Lock) Release() error {
	if l == nil || l.fl == nil {
		return nil
	}
	err := l.fl.Unlock()
	l.fl = nil
	if err != nil {
		return fmt.Errorf("lock: release %q: %w", l.path, err)
	}
	return nil
}

// MetadataPath returns the sidecar file path where Acquire writes the
// Metadata JSON for a lock at lockPath.
func MetadataPath(lockPath string) string { return lockPath + metadataSuffix }

// ReadMetadata reads the JSON metadata written by the current (or last) lock
// holder. Best-effort: a missing, empty, or malformed file returns an empty
// Metadata with no error. The OS lock is ground truth for whether the lock
// is held; this function exists only for status/debugging.
//
// The metadata lives in a sidecar file (MetadataPath(lockPath)), not in the
// lock file itself. See the package comment for the rationale.
func ReadMetadata(lockPath string) (Metadata, error) {
	b, err := os.ReadFile(MetadataPath(lockPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Metadata{}, nil
		}
		return Metadata{}, fmt.Errorf("lock: read metadata: %w", err)
	}
	if len(b) == 0 {
		return Metadata{}, nil
	}
	var m Metadata
	if err := json.Unmarshal(b, &m); err != nil {
		// Malformed metadata is non-fatal. Return zero value.
		return Metadata{}, nil
	}
	return m, nil
}

// writeMetadata writes m as JSON into the sidecar file next to lockPath.
// Best-effort: failures are returned but the caller (Acquire) ignores them.
//
// The sidecar is written via os.WriteFile (truncate-write, not atomic).
// Atomicity is unnecessary here: metadata is debugging-only and is
// overwritten by every Acquire; a partial write read by status would
// just look like malformed JSON, which ReadMetadata handles as empty.
func writeMetadata(lockPath string, m Metadata) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(MetadataPath(lockPath), b, 0644)
}
