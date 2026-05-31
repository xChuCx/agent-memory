package bench

import (
	"context"
	"path/filepath"
	"testing"

	agentgit "github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/memory"
)

// BenchmarkFetchContext_Bootstrap measures the empty-query path —
// the call every agent does at session start. Dominated by file
// reads (current.shared.md + conventions.md + index.md) plus pack
// assembly. Index is built but not queried.
func BenchmarkFetchContext_Bootstrap(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.FetchDeps{
		Idx:       idx,
		Schema:    sch,
		Manifest:  mf,
		MemoryDir: memDir,
		Branch:    agentgit.BranchInfo{},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resp, err := memory.BuildContextPack(ctx, memory.FetchRequest{}, deps)
		if err != nil {
			b.Fatal(err)
		}
		if resp.ContextMetadata.BudgetUsed == 0 {
			b.Fatal("bootstrap pack empty")
		}
	}
}

// BenchmarkFetchContext_Query measures the FTS-query path with a
// pre-warmed index. Tokens chosen from the bench vocabulary so hits
// are real (≥ a handful of matching sections).
func BenchmarkFetchContext_Query(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.FetchDeps{
		Idx:       idx,
		Schema:    sch,
		Manifest:  mf,
		MemoryDir: memDir,
		Branch:    agentgit.BranchInfo{},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memory.BuildContextPack(ctx, memory.FetchRequest{
			Query: "rotation tokens session",
		}, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFetchContext_ScopedQuery measures the same path with a
// scope filter applied — exercises the ranking-signal multiplier
// path on top of the base search.
func BenchmarkFetchContext_ScopedQuery(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.FetchDeps{
		Idx:       idx,
		Schema:    sch,
		Manifest:  mf,
		MemoryDir: memDir,
		Branch:    agentgit.BranchInfo{},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memory.BuildContextPack(ctx, memory.FetchRequest{
			Query: "session tokens",
			Scope: []string{"modules/module-005"},
		}, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFetchContext_BootstrapLargeCorpus stresses the same
// bootstrap path against the large fixture (~250 files, ~600
// sections). Useful when tuning the FTS auto-build behaviour or
// the pack assembler — small fixtures hide cost growth.
func BenchmarkFetchContext_BootstrapLargeCorpus(b *testing.B) {
	root := BuildBenchProject(b, LargeFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.FetchDeps{
		Idx:       idx,
		Schema:    sch,
		Manifest:  mf,
		MemoryDir: memDir,
		Branch:    agentgit.BranchInfo{},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memory.BuildContextPack(ctx, memory.FetchRequest{}, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}
