package bench

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/xChuCx/agent-memory/internal/memory"
)

// BenchmarkProposeUpdate_AppendPitfall measures the apply-routing
// path: add_pitfall + append_to_section to an existing section. Hits
// every phase of the orchestrator except staging (this intent applies
// inline). Each iteration appends a fresh bullet so the file grows
// linearly — confounding but representative.
func BenchmarkProposeUpdate_AppendPitfall(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.UpdateDeps{
		Manifest:  mf,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memory.ProposeUpdate(ctx,
			memory.ProposeRequest{
				Intent: memory.IntentAddPitfall,
				Operations: []memory.OperationInput{
					{
						Op:        "append_to_section",
						Path:      "pitfalls.md",
						SectionID: "pitfall-001",
						Content:   fmt.Sprintf("- Bench bullet #%d.\n", i),
					},
				},
			}, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProposeUpdate_StageDecision measures the stage-routing
// path: record_decision (always stages per default manifest). Hits
// stageProposal which writes proposal.json + target-checksums.json +
// files/<path> per iteration. Each iteration writes a fresh staging
// dir.
func BenchmarkProposeUpdate_StageDecision(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.UpdateDeps{
		Manifest:  mf,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memory.ProposeUpdate(ctx,
			memory.ProposeRequest{
				Intent:    memory.IntentRecordDecision,
				Rationale: fmt.Sprintf("bench %d", i),
				Sources:   []memory.Source{{Type: "user", Ref: "bench"}},
				Operations: []memory.OperationInput{
					{
						Op:           "append_section",
						Path:         "decisions.md",
						Heading:      fmt.Sprintf("Bench Decision %d", i),
						HeadingLevel: 2,
						Content: fmt.Sprintf(
							"## Bench Decision %d\n<!-- @id: bench-%d -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbench body %d.\n",
							i, i, i),
					},
				},
			}, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProposeUpdate_SessionLog measures the path that exercises
// the session_log intent's UTC path rewrite + apply-immediately
// routing. Each iteration appends to the same day's session file.
func BenchmarkProposeUpdate_SessionLog(b *testing.B) {
	root := BuildBenchProject(b, DefaultFixtureSize())
	mf, sch, idx := LoadDeps(b, root)
	memDir := filepath.Join(root, ".agent-memory")
	ctx := context.Background()

	deps := memory.UpdateDeps{
		Manifest:  mf,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := memory.ProposeUpdate(ctx,
			memory.ProposeRequest{
				Intent: memory.IntentSessionLog,
				Operations: []memory.OperationInput{
					{
						Op:       "create_file",
						Path:     "_", // session_log rewrites to today's session
						Content:  fmt.Sprintf("entry %d\n", i),
						IfExists: "append",
					},
				},
			}, deps)
		if err != nil {
			b.Fatal(err)
		}
	}
}
