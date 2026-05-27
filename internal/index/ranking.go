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
)

// RankingContext bundles the inputs ApplyRankingSignals needs. All fields
// are optional; an empty RankingContext leaves BM25 ordering unchanged.
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
}

// ApplyRankingSignals modifies the Score field of each result per the
// multipliers documented in design doc §20.4, then sorts results by
// ascending Score (FTS5 convention: lower = better). Returns the same
// slice for caller convenience.
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
