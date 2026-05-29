package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// stageN stages n record_decision proposals and returns their ids in
// creation order. Each gets a distinct rationale so the slugs differ;
// the dirs are renamed to deterministic, ordered ids so prefix tests
// are stable regardless of same-second timestamps.
func stageN(t *testing.T, n int) (memDir string, ids []string, deps UpdateDeps) {
	t.Helper()
	memDir, mf, sch := updateFixture(t)
	deps = UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
	for i := 0; i < n; i++ {
		rationale := string(rune('a'+i)) + "-decision"
		resp, err := ProposeUpdate(context.Background(),
			ProposeRequest{
				Intent:    IntentRecordDecision,
				Rationale: rationale,
				Sources:   []Source{{Type: "user", Ref: "t"}},
				Operations: []OperationInput{
					{
						Op:           "append_section",
						Path:         "decisions.md",
						Heading:      "D " + rationale,
						HeadingLevel: 2,
						Content:      "## D " + rationale + "\n<!-- @id: d-" + rationale + " -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbody\n",
					},
				},
			}, deps)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Status != StatusStaged {
			t.Fatalf("expected staged, got %s", resp.Status)
		}
		ids = append(ids, resp.StagingID)
	}
	return memDir, ids, deps
}

func TestResolveStagingID_Empty(t *testing.T) {
	memDir, _, _ := updateFixtureDeps(t)
	if _, err := ResolveStagingID(memDir, LatestRef); !errors.Is(err, ErrNoStaged) {
		t.Errorf("empty queue: err = %v, want ErrNoStaged", err)
	}
	if _, err := ResolveStagingID(memDir, "anything"); !errors.Is(err, ErrNoStaged) {
		t.Errorf("empty queue prefix: err = %v, want ErrNoStaged", err)
	}
}

func TestResolveStagingID_ExactMatch(t *testing.T) {
	memDir, ids, _ := stageN(t, 1)
	got, err := ResolveStagingID(memDir, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if got != ids[0] {
		t.Errorf("got %q, want %q", got, ids[0])
	}
}

func TestResolveStagingID_UniquePrefix(t *testing.T) {
	memDir, ids, _ := stageN(t, 1)
	id := ids[0]
	// First 10 chars of the timestamp prefix are very likely unique with
	// one proposal staged.
	prefix := id[:10]
	got, err := ResolveStagingID(memDir, prefix)
	if err != nil {
		t.Fatalf("prefix %q: %v", prefix, err)
	}
	if got != id {
		t.Errorf("got %q, want %q", got, id)
	}
}

func TestResolveStagingID_AmbiguousPrefix(t *testing.T) {
	memDir, ids, _ := stageN(t, 2)
	// Both ids share the "20" century prefix (and likely the whole
	// timestamp to the second). Use a prefix guaranteed to match both:
	// the common timestamp portion. Since both were staged in the same
	// test second, ids[0][:13] (date+T+HH) matches both.
	common := commonPrefix(ids[0], ids[1])
	if common == "" {
		t.Skip("ids share no common prefix; can't construct an ambiguous case")
	}
	_, err := ResolveStagingID(memDir, common)
	var amb *ErrAmbiguousPrefix
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want *ErrAmbiguousPrefix", err)
	}
	if len(amb.Candidates) < 2 {
		t.Errorf("ambiguous error should list >=2 candidates, got %v", amb.Candidates)
	}
}

func TestResolveStagingID_Latest(t *testing.T) {
	memDir, ids, _ := stageN(t, 2)
	// Rename both to deterministically-ordered ids so "latest" is
	// unambiguous regardless of same-second staging.
	older := "20260101T000000-older"
	newer := "20260201T000000-newer"
	if err := os.Rename(filepath.Join(memDir, "staging", ids[0]),
		filepath.Join(memDir, "staging", older)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(memDir, "staging", ids[1]),
		filepath.Join(memDir, "staging", newer)); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveStagingID(memDir, LatestRef)
	if err != nil {
		t.Fatal(err)
	}
	if got != newer {
		t.Errorf("latest = %q, want %q", got, newer)
	}
	// "latest" (no dashes) is also accepted.
	got2, _ := ResolveStagingID(memDir, "latest")
	if got2 != newer {
		t.Errorf("'latest' = %q, want %q", got2, newer)
	}
}

func commonPrefix(a, b string) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}
