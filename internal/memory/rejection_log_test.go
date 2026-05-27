package memory

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestListRejections_MissingLogReturnsNilNil(t *testing.T) {
	memDir := t.TempDir()
	got, err := ListRejections(memDir)
	if err != nil {
		t.Errorf("missing log should not error: %v", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil", got)
	}
}

func TestAppendRejection_AndListRoundTrip(t *testing.T) {
	memDir := t.TempDir()

	entries := []RejectionEntry{
		{
			RejectedAt: "2026-05-27T10:00:00Z",
			Reason:     RejectionReasonUser,
			StagingID:  "20260527T090000-record-decision-aaa",
			Intent:     "record_decision",
			Rationale:  "first",
			Files:      []string{"decisions.md"},
			StagedAt:   "2026-05-27T09:00:00Z",
			AgeSeconds: 3600,
		},
		{
			RejectedAt: "2026-05-27T11:00:00Z",
			Reason:     RejectionReasonTTLExpired,
			StagingID:  "20260520T120000-refresh-module-old",
			Intent:     "refresh_module",
			Rationale:  "stale",
			Files:      []string{"modules/auth.md"},
			StagedAt:   "2026-05-20T12:00:00Z",
			AgeSeconds: 604800,
		},
	}
	for _, e := range entries {
		if err := AppendRejection(memDir, e); err != nil {
			t.Fatalf("AppendRejection: %v", err)
		}
	}

	got, err := ListRejections(memDir)
	if err != nil {
		t.Fatalf("ListRejections: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	// Order must be the order of append (chronological).
	if got[0].StagingID != entries[0].StagingID {
		t.Errorf("got[0].StagingID = %q, want %q", got[0].StagingID, entries[0].StagingID)
	}
	if got[1].Reason != RejectionReasonTTLExpired {
		t.Errorf("got[1].Reason = %q, want %q", got[1].Reason, RejectionReasonTTLExpired)
	}
}

func TestAppendRejection_CreatesMetaDir(t *testing.T) {
	memDir := t.TempDir()
	// Don't pre-create meta/ — AppendRejection should mkdir.
	entry := RejectionEntry{
		RejectedAt: "2026-05-27T10:00:00Z",
		Reason:     RejectionReasonUser,
		StagingID:  "stage-1",
	}
	if err := AppendRejection(memDir, entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(memDir, "meta", "rejection-log.jsonl")); err != nil {
		t.Errorf("log file not created: %v", err)
	}
}

func TestListRejections_SkipsMalformedLines(t *testing.T) {
	memDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(memDir, "meta"), 0755); err != nil {
		t.Fatal(err)
	}
	// Write a file with one valid + one garbage + one valid line.
	logPath := filepath.Join(memDir, "meta", "rejection-log.jsonl")
	body := strings.Join([]string{
		`{"rejected_at":"2026-05-27T10:00:00Z","reason":"user_rejected","staging_id":"a"}`,
		`{this is not valid json}`,
		`{"rejected_at":"2026-05-27T11:00:00Z","reason":"ttl_expired","staging_id":"b"}`,
		``,
	}, "\n")
	if err := os.WriteFile(logPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := ListRejections(memDir)
	if err != nil {
		t.Fatalf("ListRejections: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d valid entries, want 2 (garbage line should be skipped)", len(got))
	}
	if got[0].StagingID != "a" || got[1].StagingID != "b" {
		t.Errorf("IDs = [%q, %q], want [a, b]", got[0].StagingID, got[1].StagingID)
	}
}

// TestAppendRejection_ConcurrentSafeWithinProcess — fire several goroutines
// at the log; every line must be a valid full JSON object (no torn writes
// within a process). Cross-process safety is OUT of scope — the
// .agent-memory/meta/lock advisory lock is the cross-process guard.
func TestAppendRejection_ConcurrentSafeWithinProcess(t *testing.T) {
	memDir := t.TempDir()
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_ = AppendRejection(memDir, RejectionEntry{
				RejectedAt: "2026-05-27T10:00:00Z",
				Reason:     RejectionReasonUser,
				StagingID:  "stage-many",
			})
		}(i)
	}
	wg.Wait()

	got, err := ListRejections(memDir)
	if err != nil {
		t.Fatalf("ListRejections: %v", err)
	}
	if len(got) != N {
		t.Errorf("got %d entries, want %d (some appends were lost or merged)", len(got), N)
	}
}
