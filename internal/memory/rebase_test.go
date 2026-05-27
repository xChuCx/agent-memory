package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageContentMatchProposal stages a replace_section proposal against
// pitfalls.md's "stale-lock" section. The resulting target uses
// RequireSectionContentMatch — i.e. drift can be soft (hash mismatch)
// vs hard (section disappears).
func stageContentMatchProposal(t *testing.T) (memDir, stagingID string, deps UpdateDeps) {
	t.Helper()
	memDir, mf, sch := updateFixture(t)
	deps = UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// updateFixture seeds pitfalls.md with the "stale-lock" section.
	// We pose a record_decision via replace_section_content on it —
	// but pitfalls isn't a decision; tweak intent.
	//
	// We need an intent that uses content_match AND stages. add_pitfall
	// + replace_section_content fits: add_pitfall replace routes to
	// stage per default manifest's pitfalls_replace.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentAddPitfall,
			Rationale: "rewrite stale-lock entirely",
			Operations: []OperationInput{
				{
					Op:        "replace_section_content",
					Path:      "pitfalls.md",
					SectionID: "stale-lock",
					Content:   "Updated body for the stale-lock pitfall.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("expected staged (add_pitfall + replace_section_content), got %s (%s/%s)",
			resp.Status, resp.Reason, resp.Message)
	}
	return memDir, resp.StagingID, deps
}

// mutatePitfallsBody changes the content of the "stale-lock" section
// on disk so the staged target's hash no longer matches — soft drift.
func mutatePitfallsBody(t *testing.T, memDir string) {
	t.Helper()
	path := filepath.Join(memDir, "pitfalls.md")
	mutated := []byte("# Pitfalls\n\n## Stale Lock\n<!-- @id: stale-lock -->\n\nDifferent body now.\n")
	if err := os.WriteFile(path, mutated, 0644); err != nil {
		t.Fatal(err)
	}
}

// removePitfallsSection rewrites pitfalls.md so the stale-lock section
// is gone entirely — hard block on rebase.
func removePitfallsSection(t *testing.T, memDir string) {
	t.Helper()
	path := filepath.Join(memDir, "pitfalls.md")
	without := []byte("# Pitfalls\n\nAll sections removed.\n")
	if err := os.WriteFile(path, without, 0644); err != nil {
		t.Fatal(err)
	}
}

// =============================================================================
// Happy and no-op cases
// =============================================================================

func TestRebaseStaged_NoDriftIsSkippedClean(t *testing.T) {
	_, id, deps := stageContentMatchProposal(t)
	res, err := RebaseStaged(context.Background(), id, deps, false)
	if err != nil {
		t.Fatalf("RebaseStaged: %v", err)
	}
	if res.Status != StatusSkippedClean {
		t.Errorf("Status = %q, want skipped_clean (no drift)", res.Status)
	}
	if len(res.Drift) != 0 {
		t.Errorf("expected empty Drift, got %+v", res.Drift)
	}
}

func TestRebaseStaged_SoftDriftRequiresForce(t *testing.T) {
	memDir, id, deps := stageContentMatchProposal(t)
	mutatePitfallsBody(t, memDir)

	res, err := RebaseStaged(context.Background(), id, deps, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || res.Reason != ReasonForceRequired {
		t.Errorf("Status/Reason = %q/%q, want rejected/force_required",
			res.Status, res.Reason)
	}
	if len(res.Drift) != 1 {
		t.Errorf("Drift = %+v, want one entry (the soft drift)", res.Drift)
	}
}

func TestRebaseStaged_SoftDriftForceRebases(t *testing.T) {
	memDir, id, deps := stageContentMatchProposal(t)
	mutatePitfallsBody(t, memDir)

	res, err := RebaseStaged(context.Background(), id, deps, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRebased {
		t.Fatalf("Status = %q (%s/%s), want rebased", res.Status, res.Reason, res.Message)
	}
	if !res.Forced {
		t.Errorf("Forced = false, want true")
	}
	if len(res.Files) != 1 || res.Files[0] != "pitfalls.md" {
		t.Errorf("Files = %v, want [pitfalls.md]", res.Files)
	}

	// Staged file now reflects the re-spliced content against the new
	// base (mutated body).
	staged := filepath.Join(memDir, "staging", id, "files", "pitfalls.md")
	body, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	// Two assertions: (1) heading + anchor preserved; (2) replacement
	// content from the original proposal is now in the body.
	if !strings.Contains(string(body), "<!-- @id: stale-lock -->") {
		t.Errorf("staged file lost the @id anchor:\n%s", body)
	}
	if !strings.Contains(string(body), "Updated body for the stale-lock pitfall.") {
		t.Errorf("staged file missing the proposal's replacement content:\n%s", body)
	}

	// target-checksums.json now has the refreshed hash (current section
	// hash AFTER the on-disk mutation, NOT the original stage-time hash).
	tcPath := filepath.Join(memDir, "staging", id, "target-checksums.json")
	tcBytes, _ := os.ReadFile(tcPath)
	var targets []OperationTarget
	if err := json.Unmarshal(tcBytes, &targets); err != nil {
		t.Fatal(err)
	}
	if len(targets) == 0 || targets[0].Hash == "" {
		t.Fatalf("target hash not refreshed: %+v", targets)
	}
	// The new hash should match the post-mutation section.
	src, _ := os.ReadFile(filepath.Join(memDir, "pitfalls.md"))
	wantHash := sectionHash(src, "stale-lock")
	if targets[0].Hash != wantHash {
		t.Errorf("target hash = %q, want %q (current section hash)", targets[0].Hash, wantHash)
	}
}

// =============================================================================
// Hard-block cases
// =============================================================================

func TestRebaseStaged_SectionDisappearedIsHardBlock(t *testing.T) {
	memDir, id, deps := stageContentMatchProposal(t)
	removePitfallsSection(t, memDir)

	res, err := RebaseStaged(context.Background(), id, deps, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || res.Reason != ReasonUnresolvableDrift {
		t.Errorf("Status/Reason = %q/%q, want rejected/unresolvable_drift",
			res.Status, res.Reason)
	}
	if len(res.Drift) == 0 {
		t.Error("Drift empty on hard-block rebase")
	}
}

func TestRebaseStaged_FileDisappearedIsHardBlock(t *testing.T) {
	memDir, id, deps := stageContentMatchProposal(t)
	if err := os.Remove(filepath.Join(memDir, "pitfalls.md")); err != nil {
		t.Fatal(err)
	}

	res, err := RebaseStaged(context.Background(), id, deps, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || res.Reason != ReasonUnresolvableDrift {
		t.Errorf("Status/Reason = %q/%q, want rejected/unresolvable_drift",
			res.Status, res.Reason)
	}
}

func TestRebaseStaged_UnknownID(t *testing.T) {
	_, _, deps := updateFixtureDeps(t)
	res, err := RebaseStaged(context.Background(), "no-such-id", deps, true)
	if err != nil {
		t.Fatalf("unknown id should be a Result rejection, not a Go error: %v", err)
	}
	if res.Reason != ReasonStagingNotFound {
		t.Errorf("Reason = %q, want %q", res.Reason, ReasonStagingNotFound)
	}
}

// =============================================================================
// Integration: rebase unblocks apply
// =============================================================================

// The most important integration test: simulate the full conflict
// recovery loop a user would actually go through.
//
//   1. Stage a content_match proposal.
//   2. External edit mutates the target section on disk.
//   3. apply <id> fails with target_drift.
//   4. rebase <id> (no force) fails with force_required.
//   5. rebase <id> --force succeeds.
//   6. apply <id> now succeeds.
func TestRebaseThenApply_RecoveryLoop(t *testing.T) {
	memDir, id, deps := stageContentMatchProposal(t)

	// Step 2: external edit.
	mutatePitfallsBody(t, memDir)

	// Step 3: apply fails with target_drift.
	applyRes, err := ApplyStaged(context.Background(), id, deps)
	if err != nil {
		t.Fatal(err)
	}
	if applyRes.Reason != ReasonTargetDrift {
		t.Fatalf("first apply: Reason = %q, want target_drift", applyRes.Reason)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err != nil {
		t.Fatalf("staging dir removed on drift-rejected apply: %v", err)
	}

	// Step 4: rebase without force is rejected.
	rebRes, err := RebaseStaged(context.Background(), id, deps, false)
	if err != nil {
		t.Fatal(err)
	}
	if rebRes.Reason != ReasonForceRequired {
		t.Fatalf("rebase no-force: Reason = %q, want force_required", rebRes.Reason)
	}

	// Step 5: rebase --force succeeds.
	rebRes, err = RebaseStaged(context.Background(), id, deps, true)
	if err != nil {
		t.Fatal(err)
	}
	if rebRes.Status != StatusRebased {
		t.Fatalf("rebase --force: Status = %q (%s)", rebRes.Status, rebRes.Message)
	}

	// Step 6: apply now succeeds.
	applyRes, err = ApplyStaged(context.Background(), id, deps)
	if err != nil {
		t.Fatal(err)
	}
	if applyRes.Status != StatusApplied {
		t.Fatalf("post-rebase apply Status = %q (%s/%s)",
			applyRes.Status, applyRes.Reason, applyRes.Message)
	}
	// Final on-disk state must contain the proposal's replacement content.
	final, _ := os.ReadFile(filepath.Join(memDir, "pitfalls.md"))
	if !strings.Contains(string(final), "Updated body for the stale-lock pitfall.") {
		t.Errorf("final pitfalls.md missing proposal content:\n%s", final)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err == nil {
		t.Errorf("staging dir not cleaned up after post-rebase apply")
	}
}

// =============================================================================
// Re-splice security: a manual edit that introduces a secret into the
// base is caught by the post-rebase scan.
// =============================================================================

func TestRebaseStaged_NewBaseWithSecretIsRejected(t *testing.T) {
	memDir, id, deps := stageContentMatchProposal(t)

	// External edit: still has the stale-lock section (hash differs)
	// but the SURROUNDING file body now contains a credential. After
	// re-splice the post-state has that credential — scanner must
	// reject.
	leaky := []byte("# Pitfalls\n\nLeaked: AKIAIOSFODNN7EXAMPLE\n\n## Stale Lock\n<!-- @id: stale-lock -->\n\nDifferent body.\n")
	if err := os.WriteFile(filepath.Join(memDir, "pitfalls.md"), leaky, 0644); err != nil {
		t.Fatal(err)
	}

	res, err := RebaseStaged(context.Background(), id, deps, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusRejected || res.Reason != ReasonRebaseSecret {
		t.Errorf("Status/Reason = %q/%q, want rejected/rebase_secret_detected (got message: %s)",
			res.Status, res.Reason, res.Message)
	}
	if len(res.Findings) == 0 {
		t.Errorf("Findings empty on secret rejection: %+v", res)
	}
	for _, f := range res.Findings {
		if strings.Contains(f.Type, "AKIA") || strings.Contains(f.ApproximateLocation, "AKIA") {
			t.Errorf("Finding leaked token bytes: %+v", f)
		}
	}
	// Staged file must NOT have been overwritten (rebase aborted before write).
	staged := filepath.Join(memDir, "staging", id, "files", "pitfalls.md")
	body, _ := os.ReadFile(staged)
	if strings.Contains(string(body), "AKIA") {
		t.Errorf("staged file now contains the leaked token — rebase wrote despite secret rejection")
	}
}
