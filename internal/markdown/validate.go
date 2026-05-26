package markdown

import (
	"errors"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/text"
)

// ValidateMarkdown is a thin post-splice sanity check. It parses src through
// goldmark and returns an error if the parser fails or returns a nil
// document. Goldmark accepts almost any input as best-effort Markdown, so
// this primarily catches programming errors (e.g., a splice that produced
// non-UTF-8 bytes by accident) rather than user-style malformed Markdown.
//
// Heavier structural checks (per-category schema validation, required
// sections, etc.) live in internal/schema/ from T1.9 onward.
func ValidateMarkdown(src []byte) error {
	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader(src))
	if doc == nil {
		return errors.New("validate: goldmark returned nil document")
	}
	return nil
}

// CountSections is a small convenience for callers that want to assert
// "this splice didn't accidentally drop a section" without re-doing a full
// ParseSections round trip themselves. Returns -1 on parse error.
func CountSections(src []byte) (int, error) {
	secs, err := ParseSections(src)
	if err != nil {
		return -1, fmt.Errorf("CountSections: %w", err)
	}
	return len(secs), nil
}
