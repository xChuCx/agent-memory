package index

import (
	"sort"
	"strings"
)

// Multiplier constants from design doc v0.4.1 §20.4. BM25 scores in SQLite
// FTS5 are NEGATIVE (more negative = better match), so multiplication acts
// in the natural direction:
//
//   - boost  (multiplier > 1)  → score becomes MORE negative → ranks higher
//   - penalty (multiplier < 1) → score becomes LESS negative → ranks lower
//
// Example: a result with raw BM25 score -10.0
//   ×2.0 boost   → -20.0 (better)
//   ×0.4 penalty → -4.0  (worse)
const (
	// ScopeBoost is applied to results whose file matches any of the
	// caller-supplied scope strings (substring match).
	ScopeBoost = 2.0

	// FreshBoost is applied to results in files marked fresh in the
	// FreshFiles map of RankingContext.
	FreshBoost = 1.5

	// ArchivePenalty is applied to results whose file lives under the
	// archive path prefix.
	ArchivePenalty = 0.4

	// StalePenalty is applied to results in files marked stale.
	StalePenalty = 0.6

	// ActiveBranchBoost is applied to results whose section content
	// references the active branch name (e.g., a decision scoped to a
	// feature branch). Suppressed for generic integration branches.
	ActiveBranchBoost = 1.3

	// ChangedRefBoost is applied to decisions/pitfalls whose content
	// references a file with uncommitted changes — surface the prior art
	// for whatever you're touching right now.
	ChangedRefBoost = 1.4

	// LowConfidencePenalty is applied to sections that declare a low-trust
	// confidence (inferred / stale / unknown).
	LowConfidencePenalty = 0.8
)

// RankingContext bundles the inputs ApplyRankingSignals needs. All fields
// are optional; an empty RankingContext leaves BM25 ordering unchanged.
//
// File-level signals (FreshFiles / StaleFiles, scope, archive) are keyed by
// file path ALONE. That is correct while the fetch path is local-only (PR4):
// every ranked result comes from the local store. PR5 (multi-store fetch) MUST
// re-key these by (store, file) before merging results — otherwise a file path
// present in more than one store (e.g. "decisions.md" in both local and a
// landscape store) would collect another store's freshness boost. SearchResult
// already carries Store for exactly this.
type RankingContext struct {
	// Scope: each entry is checked as a substring of the result's file
	// path. A hit applies ScopeBoost. Multiple scope entries → boost
	// applies if ANY matches (boost is applied at most once per result).
	Scope []string

	// ArchivePathPrefix marks the archive directory. Defaults to
	// "archive/" if empty. Results whose File starts with this prefix
	// receive ArchivePenalty.
	ArchivePathPrefix string

	// FreshFiles → results in these files get FreshBoost. Lookup map.
	FreshFiles map[string]bool

	// StaleFiles → results in these files get StalePenalty. Lookup map.
	StaleFiles map[string]bool

	// ActiveBranch is the current git branch name. A result whose section
	// content references it earns ActiveBranchBoost. Empty / generic
	// integration branches (main, master, …) apply no boost.
	ActiveBranch string

	// ChangedFiles are repo-relative paths with uncommitted changes. A
	// decision/pitfall section referencing any of them earns ChangedRefBoost.
	ChangedFiles []string
}

// ApplyRankingSignals modifies the Score field of each result per the
// multipliers documented in design doc §20.4, then sorts results by
// ascending Score (FTS5 convention: lower = better). Returns the same
// slice for caller convenience.
//
// File-level signals (scope, freshness, archive, stale) key off the
// result's path; content-level signals (active-branch reference,
// changed-file reference, low confidence) inspect the indexed section body
// (SearchResult.Content).
//
// Multipliers compose: a result that's in scope AND stale gets
// score × ScopeBoost × StalePenalty.
//
// The order in which multipliers are applied does not affect the final
// score (multiplication is commutative); the iteration order in this
// implementation is fixed for predictability when debugging.
func ApplyRankingSignals(results []SearchResult, rctx RankingContext) []SearchResult {
	archivePrefix := rctx.ArchivePathPrefix
	if archivePrefix == "" {
		archivePrefix = "archive/"
	}

	for i := range results {
		s := results[i].Score
		file := results[i].File
		content := results[i].Content

		if anyContains(file, rctx.Scope) {
			s *= ScopeBoost
		}
		if rctx.FreshFiles[file] {
			s *= FreshBoost
		}
		if strings.HasPrefix(file, archivePrefix) {
			s *= ArchivePenalty
		}
		if rctx.StaleFiles[file] {
			s *= StalePenalty
		}
		if referencesBranch(content, rctx.ActiveBranch) {
			s *= ActiveBranchBoost
		}
		if isDecisionOrPitfall(file) && referencesAnyPath(content, rctx.ChangedFiles) {
			s *= ChangedRefBoost
		}
		if isLowConfidence(content) {
			s *= LowConfidencePenalty
		}

		results[i].Score = s
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score < results[j].Score
	})
	return results
}

// anyContains reports whether file contains any of the candidate substrings.
// Used by scope matching. Empty candidates → returns false.
func anyContains(file string, candidates []string) bool {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if strings.Contains(file, c) {
			return true
		}
	}
	return false
}

// commonBranches are default integration branches too generic to treat as a
// meaningful "reference" — nearly all memory lives on them, so boosting on a
// name match would be noise.
var commonBranches = map[string]bool{
	"main": true, "master": true, "trunk": true, "develop": true, "dev": true,
}

// referencesBranch reports whether content mentions the active branch name.
// Generic integration branches and very short names earn no boost.
func referencesBranch(content, branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if len(b) < 3 || commonBranches[b] {
		return false
	}
	return strings.Contains(strings.ToLower(content), b)
}

// isDecisionOrPitfall reports whether file is one of the two durable
// categories the changed-file-reference signal applies to.
func isDecisionOrPitfall(file string) bool {
	return file == "decisions.md" || file == "pitfalls.md"
}

// referencesAnyPath reports whether content contains any of the given
// repo-relative paths verbatim. Empty path list → false.
func referencesAnyPath(content string, paths []string) bool {
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p != "" && strings.Contains(content, p) {
			return true
		}
	}
	return false
}

// isLowConfidence reports whether a section declares a low-trust confidence
// (inferred / stale / unknown). Sections with no Confidence field — most of
// them — are never penalized.
func isLowConfidence(content string) bool {
	switch sectionConfidence(content) {
	case "inferred", "stale", "unknown":
		return true
	default:
		return false
	}
}

// sectionConfidence extracts the value of a "Confidence: <value>" field from
// a section body, tolerating Markdown emphasis/bullet/blockquote markers
// around the label (e.g. "**Confidence:** inferred", "- Confidence: stale").
// Returns "" (lowercased first token otherwise) when no such field is present.
func sectionConfidence(content string) string {
	const cut = "*_-> \t"
	for _, line := range strings.Split(content, "\n") {
		s := strings.Trim(strings.TrimSpace(line), cut)
		if !strings.HasPrefix(strings.ToLower(s), "confidence:") {
			continue
		}
		val := strings.Trim(s[len("confidence:"):], cut)
		if idx := strings.IndexAny(val, " \t"); idx >= 0 {
			val = val[:idx]
		}
		return strings.ToLower(val)
	}
	return ""
}
