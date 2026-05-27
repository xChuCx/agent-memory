package index

import (
	"testing"
)

// rawScore is a fixed BM25-like negative score used in the multiplier tests.
const rawScore = -10.0

func mkResult(file string) SearchResult {
	return SearchResult{File: file, SectionID: "x", Score: rawScore}
}

func TestApplyRankingSignals_NoSignals(t *testing.T) {
	results := []SearchResult{mkResult("modules/auth.md")}
	out := ApplyRankingSignals(results, RankingContext{})
	if out[0].Score != rawScore {
		t.Errorf("Score = %v, want %v (no signals → unchanged)", out[0].Score, rawScore)
	}
}

func TestApplyRankingSignals_ScopeBoosts(t *testing.T) {
	results := []SearchResult{
		mkResult("modules/auth.md"),
		mkResult("modules/payments.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{Scope: []string{"auth"}})
	// "auth" matches modules/auth.md → ×2.0 → -20
	// "auth" does not appear in modules/payments.md → unchanged
	if out[0].File != "modules/auth.md" {
		t.Errorf("expected auth first after scope boost, got %s", out[0].File)
	}
	if out[0].Score != rawScore*ScopeBoost {
		t.Errorf("auth score = %v, want %v", out[0].Score, rawScore*ScopeBoost)
	}
	if out[1].Score != rawScore {
		t.Errorf("payments score = %v, want unchanged %v", out[1].Score, rawScore)
	}
}

func TestApplyRankingSignals_ArchivePenalty(t *testing.T) {
	results := []SearchResult{
		mkResult("archive/2026-05-foo.md"),
		mkResult("modules/auth.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{})
	// archive penalty: -10 × 0.4 = -4 (less negative, worse)
	// modules/auth: unchanged -10
	// After sort ascending, modules/auth first (more negative).
	if out[0].File != "modules/auth.md" {
		t.Errorf("modules should outrank archive, got %s first", out[0].File)
	}
	if out[1].Score != rawScore*ArchivePenalty {
		t.Errorf("archive score = %v, want %v", out[1].Score, rawScore*ArchivePenalty)
	}
}

func TestApplyRankingSignals_StalePenalty(t *testing.T) {
	results := []SearchResult{
		mkResult("modules/auth.md"),
		mkResult("modules/payments.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{
		StaleFiles: map[string]bool{"modules/auth.md": true},
	})
	if out[0].File != "modules/payments.md" {
		t.Errorf("fresh payments should outrank stale auth, got %s first", out[0].File)
	}
	for _, r := range out {
		if r.File == "modules/auth.md" && r.Score != rawScore*StalePenalty {
			t.Errorf("auth (stale) score = %v, want %v", r.Score, rawScore*StalePenalty)
		}
	}
}

func TestApplyRankingSignals_FreshBoost(t *testing.T) {
	results := []SearchResult{
		mkResult("modules/auth.md"),
		mkResult("modules/payments.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{
		FreshFiles: map[string]bool{"modules/payments.md": true},
	})
	if out[0].File != "modules/payments.md" {
		t.Errorf("fresh payments should rank first, got %s", out[0].File)
	}
}

func TestApplyRankingSignals_CombinedMultipliers(t *testing.T) {
	// auth: in scope + stale = ×2.0 × ×0.6 = ×1.2 = -12
	// payments: nothing = -10
	results := []SearchResult{
		mkResult("modules/auth.md"),
		mkResult("modules/payments.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{
		Scope:      []string{"auth"},
		StaleFiles: map[string]bool{"modules/auth.md": true},
	})
	// -12 < -10, so auth still wins despite stale penalty.
	if out[0].File != "modules/auth.md" {
		t.Errorf("expected auth first, got %s", out[0].File)
	}
	wantAuth := rawScore * ScopeBoost * StalePenalty
	for _, r := range out {
		if r.File == "modules/auth.md" && r.Score != wantAuth {
			t.Errorf("auth combined score = %v, want %v", r.Score, wantAuth)
		}
	}
}

func TestApplyRankingSignals_CustomArchivePrefix(t *testing.T) {
	results := []SearchResult{
		mkResult("old/foo.md"),
		mkResult("modules/auth.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{ArchivePathPrefix: "old/"})
	if out[0].File != "modules/auth.md" {
		t.Errorf("expected modules first, got %s", out[0].File)
	}
}

func TestApplyRankingSignals_EmptyScopeStringIgnored(t *testing.T) {
	// Empty scope string should not match every file (anyContains guards it).
	results := []SearchResult{
		mkResult("modules/auth.md"),
	}
	out := ApplyRankingSignals(results, RankingContext{Scope: []string{""}})
	if out[0].Score != rawScore {
		t.Errorf("empty scope shouldn't apply boost; got %v", out[0].Score)
	}
}

func TestApplyRankingSignals_SortAscending(t *testing.T) {
	results := []SearchResult{
		{File: "a.md", Score: -5.0},
		{File: "b.md", Score: -10.0},
		{File: "c.md", Score: -2.0},
	}
	out := ApplyRankingSignals(results, RankingContext{})
	// Ascending: -10 (best) → -5 → -2 (worst)
	if out[0].File != "b.md" || out[1].File != "a.md" || out[2].File != "c.md" {
		t.Errorf("sort order wrong: %v %v %v", out[0].File, out[1].File, out[2].File)
	}
}
