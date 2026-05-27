package index

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// seedQueryBench inserts n synthetic sections into a fresh index and
// returns the open handle. Each section's body contains a tracer term
// "rotate-NNN" plus shared vocabulary, so queries find matches.
func seedQueryBench(b *testing.B, n int) (*Index, string) {
	b.Helper()
	dir := b.TempDir()
	idxPath := filepath.Join(dir, "index.sqlite")
	idx, err := Open(idxPath)
	if err != nil {
		b.Fatal(err)
	}
	if err := idx.Init(context.Background()); err != nil {
		b.Fatal(err)
	}

	docs := make([]SectionDoc, n)
	for i := 0; i < n; i++ {
		docs[i] = SectionDoc{
			File:         fmt.Sprintf("modules/m-%04d.md", i),
			SectionID:    fmt.Sprintf("sec-%04d", i),
			Heading:      fmt.Sprintf("Section %04d", i),
			HeadingLevel: 2,
			Title:        fmt.Sprintf("Section %04d", i),
			Headings:     "Module > Section",
			Content: fmt.Sprintf(
				"The session token rotate-%04d on every successful request. "+
					"Tracing spans carry the tenant id propagated via header.", i),
			Tags:        "auth session tokens",
			ByteStart:   i * 1000,
			ByteEnd:     (i + 1) * 1000,
			ContentHash: fmt.Sprintf("sha256:fake-%d", i),
		}
	}
	if err := idx.UpsertSections(context.Background(), docs); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = idx.Close() })
	return idx, idxPath
}

// BenchmarkSearch_SmallIndex — 50 sections. Typical brand-new
// project's index.
func BenchmarkSearch_SmallIndex(b *testing.B) {
	idx, _ := seedQueryBench(b, 50)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := idx.Search(ctx, "session tokens", 20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearch_MediumIndex — 500 sections. Mid-project corpus.
func BenchmarkSearch_MediumIndex(b *testing.B) {
	idx, _ := seedQueryBench(b, 500)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := idx.Search(ctx, "session tokens", 20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearch_LargeIndex — 5000 sections. Stress test for
// long-lived projects with heavy decision/module history.
func BenchmarkSearch_LargeIndex(b *testing.B) {
	idx, _ := seedQueryBench(b, 5000)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := idx.Search(ctx, "session tokens", 20); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkUpsertSections_Batch10 — incremental update path. M3's
// propose_update applies UpsertSections every successful write;
// measure the per-section cost on a small batch.
func BenchmarkUpsertSections_Batch10(b *testing.B) {
	idx, _ := seedQueryBench(b, 50) // pre-warm
	ctx := context.Background()

	docs := make([]SectionDoc, 10)
	for i := range docs {
		docs[i] = SectionDoc{
			File:         "modules/bench.md",
			SectionID:    fmt.Sprintf("bench-sec-%d", i),
			Heading:      fmt.Sprintf("Bench %d", i),
			HeadingLevel: 2,
			Title:        fmt.Sprintf("Bench %d", i),
			Content:      "Updated content batch with unique marker xyzzy.",
			ByteStart:    i * 500,
			ByteEnd:      (i + 1) * 500,
			ContentHash:  fmt.Sprintf("sha256:bench-%d", i),
		}
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := idx.UpsertSections(ctx, docs); err != nil {
			b.Fatal(err)
		}
	}
}
