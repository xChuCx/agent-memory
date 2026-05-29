package index

import (
	"strings"
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

func mkResultContent(file, content string) SearchResult {
	return SearchResult{File: file, SectionID: "x", Score: rawScore, Content: content}
}

func TestApplyRankingSignals_ActiveBranchBoost(t *testing.T) {
	results := []SearchResult{
		mkResultContent("decisions.md", "We deferred this until the feature/auth-rotation branch lands."),
		mkResultContent("modules/payments.md", "Unrelated payments note."),
	}
	out := ApplyRankingSignals(results, RankingContext{ActiveBranch: "feature/auth-rotation"})
	if out[0].File != "decisions.md" {
		t.Errorf("branch-referencing section should rank first, got %s", out[0].File)
	}
	for _, r := range out {
		if r.File == "decisions.md" && r.Score != rawScore*ActiveBranchBoost {
			t.Errorf("active-branch score = %v, want %v", r.Score, rawScore*ActiveBranchBoost)
		}
		if r.File == "modules/payments.md" && r.Score != rawScore {
			t.Errorf("non-referencing score = %v, want unchanged %v", r.Score, rawScore)
		}
	}
}

func TestApplyRankingSignals_ActiveBranchGenericIgnored(t *testing.T) {
	// "main" mentioned in content must NOT boost — too generic.
	results := []SearchResult{mkResultContent("decisions.md", "the main entry point lives in cmd/")}
	out := ApplyRankingSignals(results, RankingContext{ActiveBranch: "main"})
	if out[0].Score != rawScore {
		t.Errorf("generic branch must not boost; score = %v, want %v", out[0].Score, rawScore)
	}
}

func TestApplyRankingSignals_ChangedFileRefBoost(t *testing.T) {
	results := []SearchResult{
		mkResultContent("decisions.md", "Decision about internal/index/ranking.go and its signals."),
		mkResultContent("modules/markdown-engine.md", "Also mentions internal/index/ranking.go but is a module."),
	}
	out := ApplyRankingSignals(results, RankingContext{
		ChangedFiles: []string{"internal/index/ranking.go"},
	})
	// Only the decisions.md hit gets the ×1.4 boost; the module reference does not.
	for _, r := range out {
		switch r.File {
		case "decisions.md":
			if r.Score != rawScore*ChangedRefBoost {
				t.Errorf("decision changed-ref score = %v, want %v", r.Score, rawScore*ChangedRefBoost)
			}
		case "modules/markdown-engine.md":
			if r.Score != rawScore {
				t.Errorf("module changed-ref must NOT boost; score = %v, want %v", r.Score, rawScore)
			}
		}
	}
	if out[0].File != "decisions.md" {
		t.Errorf("boosted decision should rank first, got %s", out[0].File)
	}
}

func TestApplyRankingSignals_LowConfidencePenalty(t *testing.T) {
	results := []SearchResult{
		mkResultContent("decisions.md", "## A\n<!-- @id: a -->\n**Confidence:** inferred\n\nBody."),
		mkResultContent("decisions.md", "## B\n<!-- @id: b -->\n**Confidence:** confirmed\n\nBody."),
	}
	out := ApplyRankingSignals(results, RankingContext{})
	// confirmed stays -10; inferred → -10×0.8 = -8 (worse) → ranks second.
	if out[0].SectionID != "x" || !strings.Contains(out[0].Content, "confirmed") {
		t.Errorf("confirmed section should rank first, got %q", out[0].Content)
	}
	for _, r := range out {
		if strings.Contains(r.Content, "inferred") && r.Score != rawScore*LowConfidencePenalty {
			t.Errorf("low-confidence score = %v, want %v", r.Score, rawScore*LowConfidencePenalty)
		}
	}
}

func TestSectionConfidence(t *testing.T) {
	cases := map[string]string{
		"**Confidence:** inferred":      "inferred",
		"Confidence: confirmed":         "confirmed",
		"- Confidence: stale (see #12)": "stale",
		"_Confidence:_ user-provided":   "user-provided",
		"no field here":                 "",
		"## Heading\n\nbody only":       "",
	}
	for in, want := range cases {
		if got := sectionConfidence(in); got != want {
			t.Errorf("sectionConfidence(%q) = %q, want %q", in, got, want)
		}
	}
	for _, low := range []string{"inferred", "stale", "unknown"} {
		if !isLowConfidence("Confidence: " + low) {
			t.Errorf("%q should be low confidence", low)
		}
	}
	for _, ok := range []string{"confirmed", "user-provided"} {
		if isLowConfidence("Confidence: " + ok) {
			t.Errorf("%q should NOT be low confidence", ok)
		}
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
