package bench

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agent-memory/agent-memory/internal/index"
)

// BenchmarkRebuildAll_Default measures the full index rebuild over
// the default fixture. Wipes the three tables, walks memDir,
// re-parses every Markdown file, repopulates. Useful for tracking
// `agent-memory rebuild-index` performance regressions.
func BenchmarkRebuildAll_Default(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	_, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{
			AssignMissingIDs: false,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRebuildAll_LargeCorpus uses the large fixture (~250
// files, ~600 sections). Index of how rebuild scales with corpus
// size.
func BenchmarkRebuildAll_LargeCorpus(b *testing.B) {
	root := BuildBenchProject(b, LargeFixtureSize())
	_, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{
			AssignMissingIDs: false,
		}); err != nil {
			b.Fatal(err)
		}
	}
}
