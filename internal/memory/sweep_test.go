package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// stageOneProposal runs a record_decision through ProposeUpdate (which
// stages per default manifest) and returns the resulting staging ID +
// the deps used. Reused across sweep tests.
func stageOneProposal(t *testing.T, rationale string) (memDir, stagingID string, deps UpdateDeps) {
	t.Helper()
	memDir, mf, sch := updateFixture(t)
	deps = UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: rationale,
			Sources:   []Source{{Type: "user", Ref: "sweep-test"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Sweep " + rationale,
					HeadingLevel: 2,
					Content:      "## Sweep " + rationale + "\n<!-- @id: sweep-" + rationale + " -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbody\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q, want staged", resp.Status)
	}
	return memDir, resp.StagingID, deps
}

// backdateStagedAt rewrites the proposal.json's `staged_at` field to
// nowUTC - offset, simulating an aged proposal. Returns the new value.
func backdateStagedAt(t *testing.T, memDir, stagingID string, offset time.Duration) string {
	t.Helper()
	path := filepath.Join(memDir, "staging", stagingID, "proposal.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env StagedProposal
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	newAt := time.Now().UTC().Add(-offset).Format(time.RFC3339)
	env.StagedAt = newAt
	out, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(path, out, 0644); err != nil {
		t.Fatal(err)
	}
	return newAt
}

// =============================================================================
// SweepStale
// =============================================================================

func TestSweepStale_TTLZeroIsNoop(t *testing.T) {
	memDir, _, _ := stageOneProposal(t, "a")
	res, err := SweepStale(memDir, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Expired) != 0 || len(res.Removed) != 0 {
		t.Errorf("ttl=0 should be a no-op, got %+v", res)
	}
}

func TestSweepStale_YoungProposalKept(t *testing.T) {
	memDir, id, _ := stageOneProposal(t, "young")
	res, err := SweepStale(memDir, 1*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Expired) != 0 {
		t.Errorf("young proposal flagged as expired: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err != nil {
		t.Errorf("young staging dir removed: %v", err)
	}
}

func TestSweepStale_OldProposalRemoved(t *testing.T) {
	memDir, id, _ := stageOneProposal(t, "old")
	backdateStagedAt(t, memDir, id, 2*time.Hour)

	res, err := SweepStale(memDir, 1*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Expired) != 1 || res.Expired[0].StagingID != id {
		t.Errorf("Expired = %+v, want [%s]", res.Expired, id)
	}
	if len(res.Removed) != 1 || res.Removed[0] != id {
		t.Errorf("Removed = %v, want [%s]", res.Removed, id)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err == nil {
		t.Errorf("expired staging dir still present")
	}

	// Audit log gets a ttl_expired entry.
	rs, err := ListRejections(memDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Reason != RejectionReasonTTLExpired {
		t.Errorf("log entries = %+v, want one ttl_expired", rs)
	}
}

func TestSweepStale_DryRunDoesNotRemove(t *testing.T) {
	memDir, id, _ := stageOneProposal(t, "dryrun")
	backdateStagedAt(t, memDir, id, 2*time.Hour)

	res, err := SweepStale(memDir, 1*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun {
		t.Error("DryRun=false on the result")
	}
	if len(res.Expired) != 1 {
		t.Errorf("expected one expired entry, got %+v", res.Expired)
	}
	if len(res.Removed) != 0 {
		t.Errorf("DryRun should not remove anything: %v", res.Removed)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err != nil {
		t.Errorf("staging dir gone in dry-run mode: %v", err)
	}
	// Audit log untouched.
	if rs, _ := ListRejections(memDir); len(rs) != 0 {
		t.Errorf("dry-run wrote to audit log: %+v", rs)
	}
}

func TestSweepStale_MixedAgeSet(t *testing.T) {
	memDir, oldID, deps := stageOneProposal(t, "old")
	backdateStagedAt(t, memDir, oldID, 2*time.Hour)

	// Stage a second one fresh.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: "young",
			Sources:   []Source{{Type: "user", Ref: "sweep-test"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Young",
					HeadingLevel: 2,
					Content:      "## Young\n<!-- @id: young -->\n\nfresh\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	youngID := resp.StagingID

	res, err := SweepStale(memDir, 1*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != oldID {
		t.Errorf("Removed = %v, want only [%s]", res.Removed, oldID)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", youngID)); err != nil {
		t.Errorf("young proposal accidentally removed: %v", err)
	}
}

// =============================================================================
// RejectStaged now writes to the audit log
// =============================================================================

func TestRejectStaged_AppendsToRejectionLog(t *testing.T) {
	memDir, id, _ := stageOneProposal(t, "to-reject")
	res, err := RejectStaged(memDir, id)
	if err != nil {
		t.Fatal(err)
	}
	// The interactive reject path uses Status="rejected_by_user" and
	// leaves Reason empty (Reason is reserved for the failure codes).
	if res.Reason != "" {
		t.Errorf("Reason = %q, want empty (rejected_by_user shouldn't use Reason)", res.Reason)
	}

	rs, err := ListRejections(memDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("log has %d entries, want 1", len(rs))
	}
	if rs[0].Reason != RejectionReasonUser {
		t.Errorf("Reason = %q, want %q", rs[0].Reason, RejectionReasonUser)
	}
	if rs[0].StagingID != id {
		t.Errorf("StagingID = %q, want %q", rs[0].StagingID, id)
	}
	if rs[0].Intent != string(IntentRecordDecision) {
		t.Errorf("Intent = %q, want record_decision", rs[0].Intent)
	}
	if len(rs[0].Files) == 0 || rs[0].Files[0] != "decisions.md" {
		t.Errorf("Files = %v, want [decisions.md]", rs[0].Files)
	}
}

func TestRejectStaged_UnknownIDDoesNotLog(t *testing.T) {
	memDir, _, _ := updateFixture(t)
	if _, err := RejectStaged(memDir, "no-such-id"); err != nil {
		t.Fatal(err)
	}
	rs, _ := ListRejections(memDir)
	if len(rs) != 0 {
		t.Errorf("unknown-id reject wrote to log: %+v", rs)
	}
}
