package memory

import (
	"strings"
	"unicode"
)

// Near-duplicate suppression for the search-based context pack.
//
// Design doc v0.4.1 §15.1 (step 8) / §20.5 (step 6): after ranking and
// before budget enforcement, "deduplicate semantically overlapping
// sections (Jaccard on tokens > 0.85 → keep higher-scoring)". Two ranked
// sections that say the same thing waste the caller's budget; we keep the
// higher-ranked one (which, because results arrive best-first, is whichever
// we accepted first) and drop the rest.
//
// The similarity metric is deliberately cheap and dependency-free: a set
// Jaccard over lowercased word tokens. At the project's scale (≤50 search
// results per query) the O(n²) pairwise comparison is negligible, so there
// is no need for MinHash/LSH.

// dedupeJaccardThreshold is the cut-off from the design doc. A candidate
// whose token-set Jaccard similarity to an already-accepted section is
// STRICTLY greater than this is treated as a near-duplicate and dropped.
// Identical sections score 1.0; the 0.85 margin tolerates small edits
// (a changed date line, a reworded clause) while still collapsing
// genuine repeats.
const dedupeJaccardThreshold = 0.85

// tokenize lowercases s and returns the set of its word tokens, where a
// token is a maximal run of letters/digits. Punctuation, Markdown markers,
// and whitespace are separators and contribute nothing. Returning a set
// (map) gives O(1) membership for the intersection step.
//
// Set semantics (not multiset) match Jaccard: repeated words count once.
func tokenize(s string) map[string]struct{} {
	set := make(map[string]struct{})
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			set[b.String()] = struct{}{}
			b.Reset()
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	return set
}

// jaccardSimilarity returns |A∩B| / |A∪B| for two token sets, in [0,1].
// Two empty sets (or one empty set) yield 0: there is nothing to collapse,
// and an empty section should never suppress a non-empty one.
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// Iterate the smaller set for the intersection count.
	small, large := a, b
	if len(large) < len(small) {
		small, large = large, small
	}
	inter := 0
	for tok := range small {
		if _, ok := large[tok]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// isNearDuplicate reports whether candidate is a near-duplicate of any token
// set already accepted into the pack — i.e. its similarity to at least one
// of them strictly exceeds dedupeJaccardThreshold.
func isNearDuplicate(candidate map[string]struct{}, accepted []map[string]struct{}) bool {
	for _, prev := range accepted {
		if jaccardSimilarity(candidate, prev) > dedupeJaccardThreshold {
			return true
		}
	}
	return false
}
