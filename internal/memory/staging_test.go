package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageDecision runs a record_decision proposal (which always routes to
// stage per default manifest) and returns the staging id + the deps used.
// Tests then call ApplyStaged / RejectStaged with the same deps.
func stageDecision(t *testing.T) (memDir, stagingID string, deps UpdateDeps) {
	t.Helper()
	memDir, mf, sch := updateFixture(t)
	deps = UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: "stage test decision",
			Sources:   []Source{{Type: "user", Ref: "test"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Stage Test",
					HeadingLevel: 2,
					Content:      "## Stage Test\n<!-- @id: stage-test -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nDecided.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate err: %v", err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("expected staged, got %s (%s/%s)", resp.Status, resp.Reason, resp.Message)
	}
	return memDir, resp.StagingID, deps
}

// =============================================================================
// ListStaged / LoadStaged
// =============================================================================

func TestListStaged_EmptyDirReturnsNil(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	got, err := ListStaged(memDir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d", len(got))
	}
}

func TestListStaged_MissingDirReturnsNilNil(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	// Remove staging/ entirely so the read returns ErrNotExist.
	_ = os.RemoveAll(filepath.Join(memDir, "staging"))
	got, err := ListStaged(memDir)
	if err != nil {
		t.Errorf("missing dir should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected nil, got %d", len(got))
	}
}

func TestListStaged_ReturnsStagedChronological(t *testing.T) {
	memDir, id1, deps := stageDecision(t)

	// Need a second proposal with a guaranteed-later staging id. The staging
	// id starts with YYYYMMDDTHHMMSS, so we manually move id1 backwards and
	// re-stage to get id2 > id1.
	stage1 := filepath.Join(memDir, "staging", id1)
	earlier := "19700101T000000-record-decision-earlier"
	if err := os.Rename(stage1, filepath.Join(memDir, "staging", earlier)); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// Stage another proposal — gets a fresh modern timestamp.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: "second decision",
			Sources:   []Source{{Type: "user", Ref: "test"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Second",
					HeadingLevel: 2,
					Content:      "## Second\n<!-- @id: second -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nMore.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	got, err := ListStaged(memDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ListStaged len = %d, want 2", len(got))
	}
	// Chronological: earlier first.
	if got[0].StagingID != earlier {
		t.Errorf("got[0] = %q, want %q", got[0].StagingID, earlier)
	}
	if got[1].StagingID != resp.StagingID {
		t.Errorf("got[1] = %q, want %q", got[1].StagingID, resp.StagingID)
	}
}

func TestLoadStaged_ReadsProposalEnvelope(t *testing.T) {
	memDir, id, _ := stageDecision(t)
	p, err := LoadStaged(memDir, id)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if p.StagingID != id {
		t.Errorf("StagingID = %q, want %q", p.StagingID, id)
	}
	if p.Request.Intent != IntentRecordDecision {
		t.Errorf("Intent = %q, want record_decision", p.Request.Intent)
	}
	if len(p.Files) != 1 || p.Files[0] != "decisions.md" {
		t.Errorf("Files = %v, want [decisions.md]", p.Files)
	}
}

func TestLoadStaged_MissingIDIsError(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	_, err := LoadStaged(memDir, "does-not-exist")
	if err == nil {
		t.Error("expected error for unknown staging id")
	}
}

func TestLoadStagedTargets_RoundTripsPolicy(t *testing.T) {
	memDir, id, _ := stageDecision(t)
	targets, err := LoadStagedTargets(memDir, id)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("expected at least one target")
	}
	// The append_section op produced a RequireFilePresent target (file
	// exists, no section_id since it's a parent insert).
	found := false
	for _, tgt := range targets {
		if tgt.Path == "decisions.md" {
			found = true
			// Policy round-tripped through MarshalJSON/UnmarshalJSON.
			if tgt.Policy != RequireFilePresent {
				t.Errorf("Policy = %v (%s), want RequireFilePresent", tgt.Policy, tgt.Policy)
			}
		}
	}
	if !found {
		t.Errorf("decisions.md target missing: %+v", targets)
	}
}

// =============================================================================
// CheckDrift per policy
// =============================================================================

func TestCheckDrift_RequireFilePresent_FileExists_NoDrift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	report, err := CheckDrift(memDir, OperationTarget{
		Path:   "pitfalls.md",
		Policy: RequireFilePresent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Errorf("expected no drift, got %+v", report)
	}
}

func TestCheckDrift_RequireFilePresent_FileMissing_Drift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	report, err := CheckDrift(memDir, OperationTarget{
		Path:   "nonexistent.md",
		Policy: RequireFilePresent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("expected drift report")
	}
	if report.Found != "absent" {
		t.Errorf("Found = %q, want absent", report.Found)
	}
}

func TestCheckDrift_RequireFileAbsent_FilePresent_Drift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	report, err := CheckDrift(memDir, OperationTarget{
		Path:   "pitfalls.md",
		Policy: RequireFileAbsent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("expected drift report")
	}
}

func TestCheckDrift_RequireSectionResolvable_SectionExists_NoDrift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	report, err := CheckDrift(memDir, OperationTarget{
		Path:      "pitfalls.md",
		SectionID: "stale-lock",
		Policy:    RequireSectionResolvable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Errorf("expected no drift, got %+v", report)
	}
}

func TestCheckDrift_RequireSectionResolvable_SectionMissing_Drift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	report, err := CheckDrift(memDir, OperationTarget{
		Path:      "pitfalls.md",
		SectionID: "does-not-exist",
		Policy:    RequireSectionResolvable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("expected drift report")
	}
	if !strings.Contains(report.Found, "not found") {
		t.Errorf("Found = %q, want mention of 'not found'", report.Found)
	}
}

func TestCheckDrift_RequireSectionContentMatch_HashMatches_NoDrift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	// Compute the section's current hash to feed back as expected.
	src, _ := os.ReadFile(filepath.Join(memDir, "pitfalls.md"))
	want := sectionHash(src, "stale-lock")
	report, err := CheckDrift(memDir, OperationTarget{
		Path:      "pitfalls.md",
		SectionID: "stale-lock",
		Policy:    RequireSectionContentMatch,
		Hash:      want,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report != nil {
		t.Errorf("expected no drift, got %+v", report)
	}
}

func TestCheckDrift_RequireSectionContentMatch_HashDiffers_Drift(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	report, err := CheckDrift(memDir, OperationTarget{
		Path:      "pitfalls.md",
		SectionID: "stale-lock",
		Policy:    RequireSectionContentMatch,
		Hash:      "sha256:" + strings.Repeat("0", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report == nil {
		t.Fatal("expected drift report")
	}
	if !strings.HasPrefix(report.Found, "sha256:") {
		t.Errorf("Found = %q, want sha256: prefix from current section", report.Found)
	}
}

// =============================================================================
// ApplyStaged
// =============================================================================

func TestApplyStaged_HappyPath(t *testing.T) {
	memDir, id, deps := stageDecision(t)

	// The staged file must NOT yet be on disk; only the seed is there.
	before, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if strings.Contains(string(before), "stage-test") {
		t.Fatalf("test precondition violated: decision already in file")
	}

	res, err := ApplyStaged(context.Background(), id, deps)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != StatusApplied {
		t.Fatalf("Status = %q (%s/%s), want applied", res.Status, res.Reason, res.Message)
	}

	// File on disk now contains the staged section.
	after, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if !strings.Contains(string(after), "stage-test") {
		t.Errorf("decision not applied to file: %q", after)
	}

	// Staging dir is gone.
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err == nil {
		t.Errorf("staging dir still exists after apply")
	}
}

func TestApplyStaged_UnknownID(t *testing.T) {
	_, _, deps := updateFixtureDeps(t)
	res, err := ApplyStaged(context.Background(), "no-such-id", deps)
	if err != nil {
		t.Fatalf("unknown id should not be a Go error: %v", err)
	}
	if res.Reason != ReasonStagingNotFound {
		t.Errorf("Reason = %q, want %q", res.Reason, ReasonStagingNotFound)
	}
}

func TestApplyStaged_DriftDetected(t *testing.T) {
	memDir, id, deps := stageDecision(t)

	// Mutate decisions.md AFTER stage to introduce content drift on the
	// RequireFilePresent target. Hmm — RequireFilePresent only checks
	// existence, not hash. So we need to introduce a target that uses
	// RequireSectionContentMatch. The simplest path: stage a
	// replace_section_content op (which uses content match).
	//
	// Instead of orchestrating that here, hand-craft a target-checksums.json
	// with a known-wrong hash for the existing seed section in decisions.md
	// (which has no @id, so we'll use pitfalls.md's stale-lock for clarity).
	// Then call ApplyStaged on this staging id.
	//
	// Simpler approach: corrupt target-checksums.json to add a stricter
	// target after stage. We can hand-craft.

	targetsPath := filepath.Join(memDir, "staging", id, "target-checksums.json")
	wrongTargets := []OperationTarget{
		{
			Path:      "pitfalls.md",
			SectionID: "stale-lock",
			Policy:    RequireSectionContentMatch,
			Hash:      "sha256:" + strings.Repeat("a", 64),
		},
	}
	b, _ := json.MarshalIndent(wrongTargets, "", "  ")
	if err := os.WriteFile(targetsPath, b, 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ApplyStaged(context.Background(), id, deps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != ReasonTargetDrift {
		t.Fatalf("Reason = %q, want %q", res.Reason, ReasonTargetDrift)
	}
	if len(res.Drift) == 0 {
		t.Error("expected Drift entries")
	}

	// Staging dir should NOT be cleaned up — apply was rejected.
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err != nil {
		t.Errorf("staging dir was removed despite rejection: %v", err)
	}
}

// =============================================================================
// RejectStaged
// =============================================================================

func TestRejectStaged_RemovesDir(t *testing.T) {
	memDir, id, _ := stageDecision(t)

	res, err := RejectStaged(memDir, id)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasPrefix(res.Status, "rejected") {
		t.Errorf("Status = %q, want rejected_by_user", res.Status)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err == nil {
		t.Errorf("staging dir still exists after reject")
	}
}

func TestRejectStaged_UnknownID(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	res, err := RejectStaged(memDir, "no-such-id")
	if err != nil {
		t.Fatalf("unknown id should not be a Go error: %v", err)
	}
	if res.Reason != ReasonStagingNotFound {
		t.Errorf("Reason = %q, want %q", res.Reason, ReasonStagingNotFound)
	}
}

// updateFixtureDeps wraps updateFixture and returns the deps directly.
// Used by tests that don't need an actual staged proposal.
func updateFixtureDeps(t *testing.T) (string, string, UpdateDeps) {
	t.Helper()
	memDir, mf, sch := updateFixture(t)
	return memDir, "", UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
}
