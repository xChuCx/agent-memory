package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xChuCx/agent-memory/internal/memory"
)

// TestRunStatus_PopulatesShape — the read path through the MCP layer.
// Confirms the §15.11 blocks are present and the memory version is
// threaded through.
func TestRunStatus_PopulatesShape(t *testing.T) {
	dir := proposeFixture(t)

	out, err := runStatus(context.Background(), dir, "test-version")
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if out.MemoryVersion != "test-version" {
		t.Errorf("MemoryVersion = %q, want test-version", out.MemoryVersion)
	}
	if out.Repo == "" {
		t.Error("Repo is empty")
	}
	if out.Security.LastSecretScan == "" {
		t.Error("security.last_secret_scan missing")
	}
	// mcpFixture seeds conventions/decisions/pitfalls/index + an
	// auth module, all git-tracked durable files.
	if out.DurableFiles < 3 {
		t.Errorf("DurableFiles = %d, want >= 3", out.DurableFiles)
	}
}

// TestRunStatus_ReflectsStagedProposal — stage a decision, then confirm
// memory.status reports it with drift_detected=false and a TTL window.
func TestRunStatus_ReflectsStagedProposal(t *testing.T) {
	dir := proposeFixture(t)

	// Stage a decision via the propose tool.
	resp, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent:    "record_decision",
		Rationale: "status mcp test",
		Sources:   []memory.Source{{Type: "user", Ref: "test"}},
		Operations: []memory.OperationInput{
			{
				Op:           "append_section",
				Path:         "decisions.md",
				Heading:      "Status MCP",
				HeadingLevel: 2,
				Content:      "## Status MCP\n<!-- @id: status-mcp -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nDecided.\n",
			},
		},
	})
	if err != nil {
		t.Fatalf("runProposeUpdate: %v", err)
	}
	if resp.Status != memory.StatusStaged {
		t.Fatalf("expected staged, got %s (%s)", resp.Status, resp.Reason)
	}

	out, err := runStatus(context.Background(), dir, "v")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.StagedUpdates) != 1 {
		t.Fatalf("StagedUpdates len = %d, want 1", len(out.StagedUpdates))
	}
	su := out.StagedUpdates[0]
	if su.ID != resp.StagingID {
		t.Errorf("staged ID = %q, want %q", su.ID, resp.StagingID)
	}
	if su.DriftDetected {
		t.Error("freshly-staged proposal reported as drifted")
	}
	if su.TTLRemainingSeconds <= 0 {
		t.Errorf("TTLRemainingSeconds = %d, want >0", su.TTLRemainingSeconds)
	}
}

// TestRunStatus_DetectsDriftAfterBaseEdit — stage a content-match
// proposal, mutate the base on disk, confirm status flips
// drift_detected=true. Exercises the same CheckDrift wiring apply uses.
func TestRunStatus_DetectsDriftAfterBaseEdit(t *testing.T) {
	dir := proposeFixture(t)
	memDir := filepath.Join(dir, ".agent-memory")

	// Seed pitfalls.md with an anchored section we can target + drift.
	pitfalls := filepath.Join(memDir, "pitfalls.md")
	if err := writeFileForTest(pitfalls,
		"# Pitfalls\n<!-- @id: pitfalls -->\n\n## Lock\n<!-- @id: lock -->\n\nOriginal body.\n"); err != nil {
		t.Fatal(err)
	}

	// add_pitfall + replace_section_content routes to stage
	// (pitfalls_replace) and produces a content-match target.
	resp, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent: "add_pitfall",
		Operations: []memory.OperationInput{
			{
				Op:        "replace_section_content",
				Path:      "pitfalls.md",
				SectionID: "lock",
				Content:   "Rewritten body.\n",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != memory.StatusStaged {
		t.Fatalf("expected staged, got %s (%s/%s)", resp.Status, resp.Reason, resp.Message)
	}

	// Mutate the base section so the staged target's hash no longer matches.
	if err := writeFileForTest(pitfalls,
		"# Pitfalls\n<!-- @id: pitfalls -->\n\n## Lock\n<!-- @id: lock -->\n\nDIFFERENT body now.\n"); err != nil {
		t.Fatal(err)
	}

	out, err := runStatus(context.Background(), dir, "v")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.StagedUpdates) != 1 {
		t.Fatalf("StagedUpdates len = %d, want 1", len(out.StagedUpdates))
	}
	if !out.StagedUpdates[0].DriftDetected {
		t.Error("expected drift_detected=true after base edit, got false")
	}
}

func TestRunStatus_RejectsMissingAgentMemory(t *testing.T) {
	dir := t.TempDir() // no init
	_, err := runStatus(context.Background(), dir, "v")
	if err == nil {
		t.Fatal("expected error for missing .agent-memory/")
	}
}

// writeFileForTest is a tiny helper to keep the drift test readable.
func writeFileForTest(path, body string) error {
	return os.WriteFile(path, []byte(body), 0644)
}
