package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// RejectionReason values are the wire-stable strings written to the
// audit log's `reason` field. Stable so log scrapers can filter on them.
const (
	RejectionReasonUser       = "user_rejected"
	RejectionReasonTTLExpired = "ttl_expired"
)

// RejectionEntry is one row in meta/rejection-log.jsonl, recording a
// staged proposal that was discarded (either by the user or by the TTL
// sweeper). JSON tags match the on-disk format byte-for-byte.
type RejectionEntry struct {
	RejectedAt string   `json:"rejected_at"`           // RFC3339 UTC
	Reason     string   `json:"reason"`                // one of RejectionReason* constants
	StagingID  string   `json:"staging_id"`
	Intent     string   `json:"intent,omitempty"`      // copied from the staged Request
	Rationale  string   `json:"rationale,omitempty"`
	Files      []string `json:"files,omitempty"`
	StagedAt   string   `json:"staged_at,omitempty"`   // when the proposal was originally staged
	AgeSeconds int      `json:"age_seconds,omitempty"` // RejectedAt - StagedAt
}

// rejectionLogPath returns the absolute path to the JSONL log under memDir.
func rejectionLogPath(memDir string) string {
	return filepath.Join(memDir, "meta", "rejection-log.jsonl")
}

// rejectionLogMu serialises append-writes within a single process. Cross-
// process safety is the same advisory lock everything else uses
// (meta/lock); callers that need it acquire that lock first.
var rejectionLogMu sync.Mutex

// AppendRejection serialises entry as one JSON object on its own line and
// appends it to meta/rejection-log.jsonl, creating the file (and the
// parent meta/ directory) on demand. Append is O_APPEND so concurrent
// writes within a single process get atomic per-line semantics from the
// OS; the in-process mutex above keeps the field order stable.
//
// Best-effort: a write failure does NOT cause the calling reject /
// sweep to fail. The bytes-on-disk reality is the staging dir being
// gone; the log is a downstream audit channel.
func AppendRejection(memDir string, entry RejectionEntry) error {
	rejectionLogMu.Lock()
	defer rejectionLogMu.Unlock()

	if err := os.MkdirAll(filepath.Join(memDir, "meta"), 0755); err != nil {
		return fmt.Errorf("AppendRejection: mkdir meta: %w", err)
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("AppendRejection: marshal: %w", err)
	}
	f, err := os.OpenFile(rejectionLogPath(memDir),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("AppendRejection: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("AppendRejection: write: %w", err)
	}
	return nil
}

// ListRejections reads meta/rejection-log.jsonl in full and returns the
// entries in file order (chronological because the log is append-only).
// A missing log file returns (nil, nil) — that's the normal state for a
// fresh project.
//
// Malformed JSON lines are silently skipped. This is intentional: an
// audit log is a forensic tool; a corrupted line shouldn't prevent the
// rest from being readable.
func ListRejections(memDir string) ([]RejectionEntry, error) {
	f, err := os.Open(rejectionLogPath(memDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ListRejections: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	var out []RejectionEntry
	scanner := bufio.NewScanner(f)
	// Default bufio.Scanner buffer is 64 KiB per line. Rejection-log
	// entries are tiny (~hundreds of bytes); the default is fine.
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e RejectionEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return out, fmt.Errorf("ListRejections: scan: %w", err)
	}
	return out, nil
}
