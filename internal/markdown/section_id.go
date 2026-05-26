package markdown

import (
	"fmt"
	"strings"
)

// AssignMissingIDs scans src for headings without an <!-- @id: ... --> anchor
// and injects one on the line immediately after each such heading. Returns
// the new bytes plus a slice of section IDs in document order (one entry
// per section; the value is the FINAL ID of that section, whether
// pre-existing or newly assigned).
//
// Slug rules:
//
//   - lowercase
//   - alphanumeric only (a-z, 0-9); any other rune becomes a dash
//   - runs of dashes collapse to one
//   - leading and trailing dashes stripped
//   - truncated to 64 characters (then re-trimmed if the cut left a trailing dash)
//   - empty result falls back to "section" (then made unique)
//
// Collisions are resolved by appending "-2", "-3", etc. Pre-existing
// anchors take precedence and reserve their ID even when a different
// heading would have produced the same natural slug.
//
// AssignMissingIDs is idempotent: running it on its own output produces
// byte-identical bytes (the second pass sees every section already
// anchored and generates no splice ops).
//
// Unchanged regions outside the inserted anchors are byte-identical to
// the input; the function reuses the byte-preserving Splice primitive.
func AssignMissingIDs(src []byte) ([]byte, []string, error) {
	sections, err := ParseSections(src)
	if err != nil {
		return nil, nil, fmt.Errorf("AssignMissingIDs: %w", err)
	}

	// First pass: reserve every pre-existing anchor in the used set so
	// newly-generated slugs cannot collide with them.
	used := make(map[string]bool, len(sections))
	for _, s := range sections {
		if s.AnchorID != "" {
			used[s.AnchorID] = true
		}
	}

	// Second pass: for each section without an anchor, generate a unique
	// slug and an insert-after-heading splice op.
	ids := make([]string, len(sections))
	var ops []SpliceOp
	for i, s := range sections {
		if s.AnchorID != "" {
			ids[i] = s.AnchorID
			continue
		}

		slug := slugify(s.HeadingText)
		if slug == "" {
			slug = "section"
		}
		slug = uniqueSlug(slug, used)
		ids[i] = slug

		insertAt := headingLineEnd(src, s.ByteStart)
		var anchor strings.Builder
		// If the heading line has no trailing newline (file ends mid-line),
		// emit a newline before the anchor so it lands on its own line.
		if insertAt > 0 && insertAt <= len(src) && src[insertAt-1] != '\n' {
			anchor.WriteByte('\n')
		}
		anchor.WriteString("<!-- @id: ")
		anchor.WriteString(slug)
		anchor.WriteString(" -->\n")

		ops = append(ops, SpliceOp{
			ByteStart:   insertAt,
			ByteEnd:     insertAt, // zero-width = insert
			Replacement: []byte(anchor.String()),
		})
	}

	newSrc, err := Splice(src, ops)
	if err != nil {
		return nil, nil, fmt.Errorf("AssignMissingIDs: splice: %w", err)
	}
	return newSrc, ids, nil
}

// slugify converts heading text to lowercase kebab-case per the rules above.
// May return "" if the input has no ASCII alphanumeric characters; callers
// must apply a fallback.
//
// Non-ASCII letters (e.g. Cyrillic, CJK) are treated as separators in this
// initial implementation. A future revision may add transliteration.
func slugify(text string) string {
	text = strings.ToLower(text)
	var b strings.Builder
	b.Grow(len(text))
	prevDash := false
	for _, r := range text {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		// Non-alnum becomes a single dash, but only after we've written at
		// least one alnum (no leading dash) and only if the previous output
		// was not already a dash (no consecutive dashes).
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > 64 {
		out = strings.TrimRight(out[:64], "-")
	}
	return out
}

// uniqueSlug returns base if it is not already in used; otherwise base-2,
// base-3, ... The chosen slug is added to used as a side effect so callers
// can keep passing the same map.
func uniqueSlug(base string, used map[string]bool) string {
	if !used[base] {
		used[base] = true
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

// headingLineEnd returns the byte offset immediately AFTER the newline that
// ends the heading line starting at byteStart. If the heading line has no
// trailing newline (e.g., heading at EOF without final newline), returns
// len(src) — callers must then prefix their insertion with '\n' so the
// anchor still lands on its own line.
func headingLineEnd(src []byte, byteStart int) int {
	i := byteStart
	for i < len(src) && src[i] != '\n' {
		i++
	}
	if i < len(src) {
		i++ // consume the newline
	}
	return i
}
