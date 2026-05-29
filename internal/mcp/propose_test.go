package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/memory"
)

// proposeFixture reuses mcpFixture and ensures the extra dirs the
// orchestrator touches (sessions/, staging/) exist. Returns the project
// root (NOT the memDir — runProposeUpdate joins memoryDirName itself).
func proposeFixture(t *testing.T) string {
	t.Helper()
	dir := mcpFixture(t)
	memDir := filepath.Join(dir, ".agent-memory")
	for _, sub := range []string{"sessions", "staging"} {
		if err := os.MkdirAll(filepath.Join(memDir, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Seed a local current.shared.md so update_current has a target.
	if err := os.WriteFile(
		filepath.Join(memDir, "local", "current.shared.md"),
		[]byte("# Current\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRunProposeUpdate_AppliesUpdateCurrent — the happy "apply" path through
// the MCP layer. Verifies the response surfaces Status="applied", the file
// landed on disk, and the routing trace mentions the intent.
func TestRunProposeUpdate_AppliesUpdateCurrent(t *testing.T) {
	dir := proposeFixture(t)

	out, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent: "update_current",
		Operations: []memory.OperationInput{
			{
				Op:       "create_file",
				Path:     "local/current.shared.md",
				Content:  "# Current\n\nMCP-driven body.\n",
				IfExists: "replace",
			},
		},
	})
	if err != nil {
		t.Fatalf("runProposeUpdate: %v", err)
	}
	if out.Status != memory.StatusApplied {
		t.Fatalf("Status = %q (%s/%s), want applied", out.Status, out.Reason, out.Message)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".agent-memory", "local", "current.shared.md"))
	if !strings.Contains(string(body), "MCP-driven body.") {
		t.Errorf("apply did not write file via MCP path: %q", body)
	}
	if !strings.Contains(out.Routing.Reason, "update_current") {
		t.Errorf("Routing.Reason missing intent trace: %q", out.Routing.Reason)
	}
}

// TestRunProposeUpdate_StagesRecordDecision — the happy "stage" path. The
// decisions.md file must NOT change on disk; the staging artefacts must
// exist.
func TestRunProposeUpdate_StagesRecordDecision(t *testing.T) {
	dir := proposeFixture(t)
	memDir := filepath.Join(dir, ".agent-memory")

	out, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent:    "record_decision",
		Rationale: "mcp test decision",
		Sources:   []memory.Source{{Type: "user", Ref: "test"}},
		Operations: []memory.OperationInput{
			{
				Op:           "append_section",
				Path:         "decisions.md",
				Heading:      "MCP Test Decision",
				HeadingLevel: 2,
				Content:      "## MCP Test Decision\n<!-- @id: mcp-test-decision -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nDecided here.\n",
			},
		},
	})
	if err != nil {
		t.Fatalf("runProposeUpdate: %v", err)
	}
	if out.Status != memory.StatusStaged {
		t.Fatalf("Status = %q (%s), want staged", out.Status, out.Reason)
	}
	if out.StagingID == "" {
		t.Error("StagingID empty on staged response")
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", out.StagingID, "proposal.json")); err != nil {
		t.Errorf("staging proposal.json missing: %v", err)
	}
	// decisions.md on disk must still NOT contain the new section.
	on, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if strings.Contains(string(on), "mcp-test-decision") {
		t.Errorf("decision leaked to disk on stage: %q", on)
	}
}

// TestRunProposeUpdate_RejectsInvalidIntent — rejection is a successful
// JSON-RPC response, not a transport error. Verify that error is nil but
// Status="rejected".
func TestRunProposeUpdate_RejectsInvalidIntent(t *testing.T) {
	dir := proposeFixture(t)
	out, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent: "not_a_real_intent",
		Operations: []memory.OperationInput{
			{Op: "create_file", Path: "local/current.shared.md", Content: "# x\n", IfExists: "replace"},
		},
	})
	if err != nil {
		t.Fatalf("rejection should not be a transport error: %v", err)
	}
	if out.Status != memory.StatusRejected {
		t.Errorf("Status = %q, want rejected", out.Status)
	}
	if out.Reason != memory.ReasonInvalidIntent {
		t.Errorf("Reason = %q, want %q", out.Reason, memory.ReasonInvalidIntent)
	}
}

// TestRunProposeUpdate_RejectsSecretInBody — secret_detected propagates
// through the MCP layer with Findings populated and NO token bytes leaking
// into the response fields.
func TestRunProposeUpdate_RejectsSecretInBody(t *testing.T) {
	dir := proposeFixture(t)

	out, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent: "update_current",
		Operations: []memory.OperationInput{
			{
				Op:       "create_file",
				Path:     "local/current.shared.md",
				Content:  "# Current\n\nKey: AKIAIOSFODNN7EXAMPLE\n",
				IfExists: "replace",
			},
		},
	})
	if err != nil {
		t.Fatalf("rejection should not be a transport error: %v", err)
	}
	if out.Status != memory.StatusRejected {
		t.Fatalf("Status = %q, want rejected", out.Status)
	}
	if out.Reason != memory.ReasonSecretDetected {
		t.Errorf("Reason = %q, want %q", out.Reason, memory.ReasonSecretDetected)
	}
	if len(out.Findings) == 0 {
		t.Fatal("expected at least one finding on secret rejection")
	}
	for _, f := range out.Findings {
		if strings.Contains(f.Type, "AKIA") || strings.Contains(f.ApproximateLocation, "AKIA") {
			t.Errorf("Finding leaked token bytes via MCP wire types: %+v", f)
		}
	}
}

// TestRunProposeUpdate_RejectsMissingAgentMemory — environmental rejection
// (no .agent-memory/ in root) IS a transport error, because the orchestrator
// can't even load the manifest. Symmetric with TestRunFetchContext.
func TestRunProposeUpdate_RejectsMissingAgentMemory(t *testing.T) {
	dir := t.TempDir() // no init
	_, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent: "update_current",
		Operations: []memory.OperationInput{
			{Op: "create_file", Path: "local/current.shared.md", Content: "# x\n", IfExists: "replace"},
		},
	})
	if err == nil {
		t.Fatal("expected error for missing .agent-memory/")
	}
}

// TestRunProposeUpdate_SessionLogRewritesPathThroughMCP — covers T3.10 via
// the MCP entry point. The agent supplies a non-sessions/ path; the
// orchestrator rewrites it to today's session file.
func TestRunProposeUpdate_SessionLogRewritesPathThroughMCP(t *testing.T) {
	dir := proposeFixture(t)
	out, err := runProposeUpdate(context.Background(), dir, nil, ProposeUpdateInput{
		Intent: "session_log",
		Operations: []memory.OperationInput{
			{
				Op:       "create_file",
				Path:     "local/current.shared.md", // will be rewritten
				Content:  "# Today\n\nLog entry.\n",
				IfExists: "append",
			},
		},
	})
	if err != nil {
		t.Fatalf("runProposeUpdate: %v", err)
	}
	if out.Status != memory.StatusApplied {
		t.Fatalf("Status = %q (%s/%s)", out.Status, out.Reason, out.Message)
	}
	if len(out.Files) == 0 || !strings.HasPrefix(out.Files[0], "sessions/") {
		t.Errorf("session_log did not rewrite path, Files = %v", out.Files)
	}
}
