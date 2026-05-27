package memory

import (
	"fmt"
	"regexp"
	"sort"
)

// AllowlistRegion is a byte range [ByteStart, ByteEnd) in scanned content
// that the secret scanner must skip. Produced by ExtractAllowlistRegions
// from paired HTML-comment markers in Markdown source.
type AllowlistRegion struct {
	ByteStart int    `json:"byte_start"`
	ByteEnd   int    `json:"byte_end"`
	Reason    string `json:"reason"`
}

// Marker regexes. The open marker MUST include a non-empty reason="..."
// attribute so post-mortem audits know why a region was excluded.
var (
	allowOpenRe = regexp.MustCompile(`<!--\s*@secret-scan:\s*allow(?:\s+reason="([^"]*)")?\s*-->`)
	allowEndRe  = regexp.MustCompile(`<!--\s*@secret-scan:\s*end\s*-->`)
)

// ExtractAllowlistRegions scans content for paired allowlist markers and
// returns the byte ranges between them.
//
// The marker format (verbatim):
//
//   <!-- @secret-scan: allow reason="documentation example" -->
//   ...allowed content...
//   <!-- @secret-scan: end -->
//
// Region boundaries: ByteStart is the offset right after the opening
// marker's '>' character; ByteEnd is the offset of the closing marker's
// '<' character. The markers themselves are NOT inside the allowed range
// (so a secret that happened to live in the comment text would still be
// flagged — but markers don't look like secrets in practice).
//
// Errors:
//   - open marker without a closing one in scope
//   - close marker with no preceding open
//   - nested open (a second open before the first has closed)
//   - empty reason on an open marker
//
// An empty input or input without any markers returns nil regions and nil
// error.
func ExtractAllowlistRegions(content []byte) ([]AllowlistRegion, error) {
	type marker struct {
		start, end int
		kind       string // "open" | "end"
		reason     string
	}
	var markers []marker

	for _, m := range allowOpenRe.FindAllSubmatchIndex(content, -1) {
		reason := ""
		// The capture group for reason is at m[2:4]; absent when the open
		// marker had no reason attribute (which we reject below).
		if m[2] >= 0 && m[3] >= 0 {
			reason = string(content[m[2]:m[3]])
		}
		markers = append(markers, marker{
			start:  m[0],
			end:    m[1],
			kind:   "open",
			reason: reason,
		})
	}
	for _, m := range allowEndRe.FindAllIndex(content, -1) {
		markers = append(markers, marker{
			start: m[0],
			end:   m[1],
			kind:  "end",
		})
	}

	if len(markers) == 0 {
		return nil, nil
	}

	sort.Slice(markers, func(i, j int) bool { return markers[i].start < markers[j].start })

	var (
		regions []AllowlistRegion
		open    *marker
	)
	for i := range markers {
		m := markers[i]
		if m.kind == "open" {
			if open != nil {
				return nil, fmt.Errorf("allowlist: nested @secret-scan:allow at byte %d (previous open at byte %d)",
					m.start, open.start)
			}
			if m.reason == "" {
				return nil, fmt.Errorf("allowlist: @secret-scan:allow at byte %d has empty or missing reason= attribute",
					m.start)
			}
			open = &markers[i]
			continue
		}
		// end marker
		if open == nil {
			return nil, fmt.Errorf("allowlist: @secret-scan:end at byte %d has no matching open",
				m.start)
		}
		regions = append(regions, AllowlistRegion{
			ByteStart: open.end, // first byte AFTER the open marker
			ByteEnd:   m.start,  // byte index of the end marker's '<'
			Reason:    open.reason,
		})
		open = nil
	}
	if open != nil {
		return nil, fmt.Errorf("allowlist: @secret-scan:allow at byte %d has no matching end",
			open.start)
	}
	return regions, nil
}

// IsAllowlisted reports whether the byte range [start, end) is fully
// contained in any of regions.
func IsAllowlisted(start, end int, regions []AllowlistRegion) bool {
	for _, r := range regions {
		if start >= r.ByteStart && end <= r.ByteEnd {
			return true
		}
	}
	return false
}
