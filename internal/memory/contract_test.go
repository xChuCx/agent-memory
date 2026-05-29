package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Confidence default
// =============================================================================

func TestProposeUpdate_ConfidenceDefaultsToInferred(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// record_decision stages; omit confidence → should default to "inferred"
	// and be recorded in the staged proposal.json.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:  IntentRecordDecision,
			Sources: []Source{{Type: "user", Ref: "t"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Conf Default",
					HeadingLevel: 2,
					Content:      "## Conf Default\n<!-- @id: conf-default -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbody\n",
				},
			},
			// Confidence omitted.
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q (%s)", resp.Status, resp.Reason)
	}
	// The staged proposal.json must record confidence = "inferred".
	p, err := LoadStaged(memDir, resp.StagingID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Request.Confidence != "inferred" {
		t.Errorf("staged confidence = %q, want inferred (default)", p.Request.Confidence)
	}
}

func TestProposeUpdate_ConfidenceExplicitPreserved(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:     IntentRecordDecision,
			Confidence: "user-provided",
			Sources:    []Source{{Type: "user", Ref: "t"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Conf Explicit",
					HeadingLevel: 2,
					Content:      "## Conf Explicit\n<!-- @id: conf-explicit -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbody\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := LoadStaged(memDir, resp.StagingID)
	if p.Request.Confidence != "user-provided" {
		t.Errorf("explicit confidence overwritten: got %q", p.Request.Confidence)
	}
}

// =============================================================================
// if_exists=replace force-stage (durable) vs auto-apply (ephemeral)
// =============================================================================

func TestProposeUpdate_ReplaceOnDurableForcesStage(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// conventions.md is git-tracked (durable). Simulate a user who set
	// the conventions category to auto-apply — §15.3 must still force a
	// wholesale create_file replace to stage regardless of that policy.
	mf.Updates.Approval.Conventions = "apply"
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:  IntentUpdateConventions,
			Sources: []Source{{Type: "user", Ref: "t"}},
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "conventions.md",
					Content:  "# Conventions\n<!-- @id: conventions -->\n\nrewritten wholesale\n",
					IfExists: "replace",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q, want staged (durable replace forces stage)", resp.Status)
	}
	if !strings.Contains(resp.Routing.Reason, "if_exists=replace") {
		t.Errorf("routing reason missing replace-force note: %q", resp.Routing.Reason)
	}
}

func TestProposeUpdate_ReplaceOnEphemeralStillApplies(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// local/current.shared.md is git_tracked=false (ephemeral). Wholesale
	// replace stays auto-apply — the intent table marks update_current
	// auto-apply and replace is the normal mode for local state.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent,
			Operations: []OperationInput{
				{
					Op:       "create_file",
					Path:     "local/current.shared.md",
					Content:  "# Current\n\nfresh state\n",
					IfExists: "replace",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s), want applied (ephemeral replace auto-applies)", resp.Status, resp.Reason)
	}
}

// =============================================================================
// §15.2 output shapes
// =============================================================================

func TestProposeUpdate_AppliedOutputShape(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// add_pitfall + append_to_section applies and touches a section with an id.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentAddPitfall,
			Operations: []OperationInput{
				{
					Op:        "append_to_section",
					Path:      "pitfalls.md",
					SectionID: "stale-lock",
					Content:   "- another bullet\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s)", resp.Status, resp.Reason)
	}
	if resp.AppliedAt == "" {
		t.Error("applied_at empty")
	}
	if _, err := time.Parse(time.RFC3339, resp.AppliedAt); err != nil {
		t.Errorf("applied_at not RFC3339: %q (%v)", resp.AppliedAt, err)
	}
	// append_to_section's target carries the section id → affected_sections.
	found := false
	for _, as := range resp.AffectedSections {
		if as.File == "pitfalls.md" && as.SectionID == "stale-lock" {
			found = true
		}
	}
	if !found {
		t.Errorf("affected_sections missing pitfalls.md/stale-lock: %+v", resp.AffectedSections)
	}
	if resp.Warnings == nil {
		t.Error("warnings should be non-nil (empty slice) on apply")
	}
}

func TestProposeUpdate_StagedOutputShape(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:  IntentRecordDecision,
			Sources: []Source{{Type: "user", Ref: "t"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Shape",
					HeadingLevel: 2,
					Content:      "## Shape\n<!-- @id: shape -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbody\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q", resp.Status)
	}
	if resp.StagingTTLSeconds != mf.Staging.TTLSeconds {
		t.Errorf("staging_ttl_seconds = %d, want %d", resp.StagingTTLSeconds, mf.Staging.TTLSeconds)
	}
	if !resp.HumanApprovalRequired {
		t.Error("human_approval_required should be true on stage")
	}
	want := "agent-memory review " + resp.StagingID
	if resp.ReviewCommand != want {
		t.Errorf("review_command = %q, want %q", resp.ReviewCommand, want)
	}
}

// guards against accidental JSON-shape regressions: applied response
// round-trips through json with the §15.2 field names.
func TestProposeResponse_AppliedJSONFieldNames(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentSessionLog,
			Operations: []OperationInput{
				{Op: "create_file", Path: "_", Content: "log\n", IfExists: "append"},
			},
		}, deps)
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s)", resp.Status, resp.Reason)
	}
	b, _ := json.Marshal(resp)
	// status + applied_at always present on apply. index_updated is
	// omitempty (this test has no Idx wired, so it's legitimately
	// omitted); not asserted here.
	for _, key := range []string{`"status"`, `"applied_at"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("applied JSON missing %s: %s", key, b)
		}
	}
	// session file was written.
	want := "sessions/" + time.Now().UTC().Format("2006-01-02") + ".md"
	if _, err := os.Stat(filepath.Join(memDir, filepath.FromSlash(want))); err != nil {
		t.Errorf("session file not written: %v", err)
	}
}
