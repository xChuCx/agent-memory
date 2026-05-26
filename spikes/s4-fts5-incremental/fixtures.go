package s4

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GeneratedFiles lists the file paths in the order that Generate distributes
// sections across them (round-robin via index mod len). Exposed so tests can
// pick a (file, section_id) pair that actually exists, e.g.:
//
//   idx := 8
//   existingFile := GeneratedFiles[idx%len(GeneratedFiles)]
//   existingSection := fmt.Sprintf("section-%04d", idx)
var GeneratedFiles = []string{
	"modules/auth.md",
	"modules/payments.md",
	"modules/search.md",
	"modules/billing.md",
	"modules/frontend.md",
	"decisions.md",
	"pitfalls.md",
	"conventions.md",
}

// FileForIndex returns the file path that Generate(n)[i] uses for any n > i.
func FileForIndex(i int) string {
	return GeneratedFiles[i%len(GeneratedFiles)]
}

// SectionIDForIndex returns the section_id Generate(n)[i] assigns.
func SectionIDForIndex(i int) string {
	return fmt.Sprintf("section-%04d", i)
}

// Generate produces n synthetic sections distributed across GeneratedFiles
// round-robin, with overlapping keyword vocabulary so search queries return
// ranked results. Section IDs are sequential to make test selection
// deterministic.
func Generate(n int) []Section {
	keywords := []string{
		"authentication", "authorization", "token", "refresh",
		"payment", "stripe", "webhook", "idempotency",
		"search", "indexing", "ranking", "query",
		"billing", "invoice", "subscription", "renewal",
		"frontend", "routing", "hydration", "rendering",
	}

	sections := make([]Section, n)
	for i := 0; i < n; i++ {
		file := FileForIndex(i)
		kw1 := keywords[i%len(keywords)]
		kw2 := keywords[(i*7)%len(keywords)]
		heading := fmt.Sprintf("Section %d: %s and %s", i, kw1, kw2)
		content := fmt.Sprintf(
			"This section discusses %s in the context of %s. "+
				"The implementation uses standard patterns and is covered by tests. "+
				"Sources: internal/%s.go, internal/%s_test.go. "+
				"Considerations include performance, correctness, and observability.",
			kw1, kw2, kw1, kw1,
		)
		hash := sha256.Sum256([]byte(content))
		sections[i] = Section{
			File:         file,
			SectionID:    SectionIDForIndex(i),
			Heading:      heading,
			HeadingLevel: 2,
			Title:        heading,
			Headings:     heading,
			Content:      content,
			Tags:         kw1 + " " + kw2,
			ByteStart:    i * 256,
			ByteEnd:      (i + 1) * 256,
			ContentHash:  "sha256:" + hex.EncodeToString(hash[:]),
		}
	}
	return sections
}
