package markdown

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// buildMarkdown emits a Markdown document with n level-2 sections,
// each with an @id anchor and `bodyChars` bytes of pseudo-prose.
// Deterministic via seed.
func buildMarkdown(seed int64, n, bodyChars int) []byte {
	rng := rand.New(rand.NewSource(seed))
	words := []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
		"system", "must", "handle", "retry", "with", "backoff", "tokens"}
	var b strings.Builder
	b.WriteString("# Top\n<!-- @id: top -->\n\nintro\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "## Section %03d\n<!-- @id: sec-%03d -->\n\n", i, i)
		var line strings.Builder
		for body := 0; body < bodyChars; {
			w := words[rng.Intn(len(words))]
			if line.Len()+len(w)+1 > 80 {
				b.WriteString(line.String())
				b.WriteByte('\n')
				body += line.Len() + 1
				line.Reset()
			}
			if line.Len() > 0 {
				line.WriteByte(' ')
			}
			line.WriteString(w)
		}
		if line.Len() > 0 {
			b.WriteString(line.String())
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// BenchmarkParseSections measures ParseSections on a "typical" decisions-
// sized file (20 sections × 400 chars ≈ 8 KB).
func BenchmarkParseSections(b *testing.B) {
	src := buildMarkdown(1, 20, 400)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ParseSections(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseSections_LargeFile measures a heavier file (100
// sections × 600 chars ≈ 60 KB) — closer to a long-lived module
// or aggregated archive entry.
func BenchmarkParseSections_LargeFile(b *testing.B) {
	src := buildMarkdown(2, 100, 600)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := ParseSections(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSplice_SingleOp measures one byte-range splice on a
// medium file. Excludes ParseSections cost (we pre-compute the
// section offsets).
func BenchmarkSplice_SingleOp(b *testing.B) {
	src := buildMarkdown(3, 20, 400)
	sections, err := ParseSections(src)
	if err != nil {
		b.Fatal(err)
	}
	if len(sections) < 5 {
		b.Fatalf("need >=5 sections, got %d", len(sections))
	}
	target := sections[3]
	replacement := []byte("## Replaced 003\n<!-- @id: sec-003 -->\n\nshort body.\n\n")

	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Splice(src, []SpliceOp{{
			ByteStart:   target.ByteStart,
			ByteEnd:     target.ByteEnd,
			Replacement: replacement,
		}}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFindByID measures the linear scan FindByID does over
// the sections slice. With ~120 sections we expect O(N) but still
// nanoseconds.
func BenchmarkFindByID(b *testing.B) {
	src := buildMarkdown(4, 120, 200)
	sections, err := ParseSections(src)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := FindByID(sections, "sec-080"); !ok {
			b.Fatal("not found")
		}
	}
}
