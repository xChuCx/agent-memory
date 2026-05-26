// Package markdown implements the byte-preserving Markdown engine that the
// agent-memory project uses to read and modify .agent-memory/ files without
// reformatting unchanged regions.
//
// The pattern: parse the AST only to locate byte offsets, never use it to
// render output. Splices are byte-level substring replacements on the
// original source. See docs/patterns/byte-preserving-engine.md.
//
// This package is the production version of spike S1. The spike code lives
// at spikes/s1-byte-preserving-markdown/.
package markdown

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Section represents a Markdown section located in source bytes.
//
// A "section" runs from a heading line (inclusive) up to the start of the
// next heading at the same or higher level — or to the end of the source if
// no such heading follows. Nested headings produce nested sections; a level-2
// section therefore subsumes any level-3+ sections beneath it.
type Section struct {
	HeadingText  string
	HeadingLevel int
	AnchorID     string // empty if no <!-- @id: ... --> anchor follows the heading
	Occurrence   int    // 1-based among sections with the same (HeadingText, HeadingLevel)
	ByteStart    int    // inclusive; start of the heading line
	ByteEnd      int    // exclusive; start of next heading at same/higher level, or len(src)
	ContentHash  string // "sha256:<hex>" of bytes [ByteStart, ByteEnd)
}

// ParseSections walks the goldmark AST and returns sections with byte offsets
// in document order. Headings inside fenced code blocks are not returned —
// goldmark recognises them as code and does not emit them as headings.
//
// Every Section's ContentHash is computed against the current src, so a
// caller comparing hashes across writes detects byte-level drift.
func ParseSections(src []byte) ([]Section, error) {
	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader(src))

	type hinfo struct {
		text       string
		level      int
		byteStart  int
		occurrence int
		anchorID   string
	}
	var headings []hinfo
	occCount := make(map[string]int)

	if err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		title := extractText(h, src)

		lines := h.Lines()
		if lines == nil || lines.Len() == 0 {
			return ast.WalkContinue, nil
		}
		firstSeg := lines.At(0)
		byteStart := lineStart(src, firstSeg.Start)

		occCount[title]++
		headings = append(headings, hinfo{
			text:       title,
			level:      h.Level,
			byteStart:  byteStart,
			occurrence: occCount[title],
		})
		return ast.WalkContinue, nil
	}); err != nil {
		return nil, fmt.Errorf("ast walk: %w", err)
	}

	for i := range headings {
		headings[i].anchorID = findAnchorID(src, headings[i].byteStart)
	}

	sections := make([]Section, len(headings))
	for i, h := range headings {
		end := len(src)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].level <= h.level {
				end = headings[j].byteStart
				break
			}
		}
		sections[i] = Section{
			HeadingText:  h.text,
			HeadingLevel: h.level,
			AnchorID:     h.anchorID,
			Occurrence:   h.occurrence,
			ByteStart:    h.byteStart,
			ByteEnd:      end,
			ContentHash:  hashBytes(src[h.byteStart:end]),
		}
	}
	return sections, nil
}

// FindByID returns a pointer to the first section with the given anchor ID,
// or (nil, false) if not found.
func FindByID(sections []Section, id string) (*Section, bool) {
	for i := range sections {
		if sections[i].AnchorID == id {
			return &sections[i], true
		}
	}
	return nil, false
}

// FindByHeading returns a pointer to the section matching (text, level,
// occurrence), or (nil, false) if not found. Occurrence is 1-based; pass 1
// for "first match".
func FindByHeading(sections []Section, heading string, level, occurrence int) (*Section, bool) {
	for i := range sections {
		s := &sections[i]
		if s.HeadingText == heading && s.HeadingLevel == level && s.Occurrence == occurrence {
			return s, true
		}
	}
	return nil, false
}

// hashBytes returns "sha256:<hex>" for b. Used for Section.ContentHash.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// extractText pulls the plain heading text from a Heading node by walking
// its Text/String descendants.
func extractText(h *ast.Heading, src []byte) string {
	var buf bytes.Buffer
	_ = ast.Walk(h, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Text:
			buf.Write(v.Segment.Value(src))
		case *ast.String:
			buf.Write(v.Value)
		}
		return ast.WalkContinue, nil
	})
	return buf.String()
}

// lineStart walks backward from offset to the start of its line (0 or byte
// after '\n').
func lineStart(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	for offset > 0 && src[offset-1] != '\n' {
		offset--
	}
	return offset
}

// findAnchorID looks for an <!-- @id: ... --> anchor immediately following
// the heading line at headingStart. Returns the trimmed ID or "".
//
// Convention (matches design doc v0.4.1 §12.1 and spike S1's strict rule):
// the anchor sits on the line directly after the heading. One blank line of
// slack is tolerated for human-edited files. The scan does NOT cross into
// the next heading or any non-blank, non-anchor content — preventing the
// cross-section leak that spike S1 fixture 02 originally exposed.
//
// The closing "-->" must appear on the same line as the opening "<!-- @id:".
func findAnchorID(src []byte, headingStart int) string {
	i := headingStart
	for i < len(src) && src[i] != '\n' {
		i++
	}
	if i < len(src) {
		i++
	}

	// Allow at most one blank line between heading and anchor.
	if i < len(src) && src[i] == '\n' {
		i++
	}
	if i >= len(src) {
		return ""
	}

	openMarker := []byte("<!-- @id:")
	if !bytes.HasPrefix(src[i:], openMarker) {
		return ""
	}

	rest := src[i+len(openMarker):]
	if lineEnd := bytes.IndexByte(rest, '\n'); lineEnd >= 0 {
		rest = rest[:lineEnd]
	}
	closeIdx := bytes.Index(rest, []byte("-->"))
	if closeIdx < 0 {
		return ""
	}
	return string(bytes.TrimSpace(rest[:closeIdx]))
}
