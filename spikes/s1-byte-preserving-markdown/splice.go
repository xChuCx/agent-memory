// Package s1 demonstrates byte-preserving Markdown section splicing using goldmark
// for AST-based location only. The renderer is never invoked; unchanged regions of
// the source are guaranteed byte-identical to the input.
//
// This is Spike S1 of the agent-memory project.
// See ../../docs/spikes/s1-results.md and ../../docs/patterns/byte-preserving-engine.md.
package s1

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
type Section struct {
	HeadingText  string
	HeadingLevel int
	AnchorID     string // empty if no <!-- @id: ... --> anchor follows the heading
	Occurrence   int    // 1-based among sections with same (HeadingText, HeadingLevel)
	ByteStart    int    // inclusive; start of the heading line
	ByteEnd      int    // exclusive; start of next heading at same/higher level, or len(src)
}

// ContentHash returns the sha256 hex of the section's bytes in src, prefixed with "sha256:".
func (s Section) ContentHash(src []byte) string {
	sum := sha256.Sum256(src[s.ByteStart:s.ByteEnd])
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ParseSections walks the AST and returns sections with byte offsets, in document order.
// Headings inside fenced code blocks are not returned — goldmark already filters them out.
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

	walkErr := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
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
	})
	if walkErr != nil {
		return nil, fmt.Errorf("ast walk: %w", walkErr)
	}

	// Resolve @id anchors that follow each heading line, within a bounded window.
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
		}
	}
	return sections, nil
}

// FindByID returns the first section with the given anchor ID.
func FindByID(sections []Section, id string) (Section, bool) {
	for _, s := range sections {
		if s.AnchorID == id {
			return s, true
		}
	}
	return Section{}, false
}

// FindByHeading returns the section matching (text, level, occurrence).
// Occurrence is 1-based; pass 1 for "first match".
func FindByHeading(sections []Section, heading string, level, occurrence int) (Section, bool) {
	for _, s := range sections {
		if s.HeadingText == heading && s.HeadingLevel == level && s.Occurrence == occurrence {
			return s, true
		}
	}
	return Section{}, false
}

// Splice replaces src[start:end] with replacement and returns the new bytes.
// It validates the range and panics on neither input.
func Splice(src []byte, start, end int, replacement []byte) ([]byte, error) {
	if start < 0 || end > len(src) || start > end {
		return nil, fmt.Errorf("invalid splice range [%d, %d) for src of length %d", start, end, len(src))
	}
	out := make([]byte, 0, len(src)-(end-start)+len(replacement))
	out = append(out, src[:start]...)
	out = append(out, replacement...)
	out = append(out, src[end:]...)
	return out, nil
}

// ReplaceSection splices the entire section (heading line through last byte before
// the next section, or EOF) with newContent.
func ReplaceSection(src []byte, sec Section, newContent []byte) ([]byte, error) {
	return Splice(src, sec.ByteStart, sec.ByteEnd, newContent)
}

// extractText pulls the plain heading text from a Heading node.
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

// lineStart walks backward from offset to the start of its line (0 or byte after '\n').
func lineStart(src []byte, offset int) int {
	if offset > len(src) {
		offset = len(src)
	}
	for offset > 0 && src[offset-1] != '\n' {
		offset--
	}
	return offset
}

// findAnchorID looks for an <!-- @id: ... --> anchor immediately following the
// heading line at headingStart. Returns the trimmed ID or "".
//
// Convention (matches design doc §12.1): the anchor sits on the line directly
// after the heading. We tolerate at most ONE blank line of slack for human-edited
// files. We do NOT scan past the next non-blank, non-anchor line — this prevents
// false matches against anchors that belong to subsequent sections.
//
// The closing "-->" must appear on the same line as the opening "<!-- @id:" —
// multi-line anchors are out of scope.
func findAnchorID(src []byte, headingStart int) string {
	// Advance past the heading line.
	i := headingStart
	for i < len(src) && src[i] != '\n' {
		i++
	}
	if i < len(src) {
		i++ // skip the newline
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

	// Find closing --> on the same line.
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
