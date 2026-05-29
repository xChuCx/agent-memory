package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// archive_section end-to-end through the orchestrator
// =============================================================================

// TestProposeUpdate_ArchiveSection_ForcesStageAndCopies — archive_section
// under an apply-routing intent (update_current) must be FORCED to stage,
// and the staged file set must include both the rewritten source and the
// new archive file carrying the original content.
func TestProposeUpdate_ArchiveSection_ForcesStageAndCopies(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentUpdateCurrent, // would normally APPLY
			Operations: []OperationInput{
				{
					Op:          "archive_section",
					Path:        "pitfalls.md",
					SectionID:   "stale-lock",
					ArchivePath: "archive/2026-05-stale-lock.md",
					Replacement: "## Stale Lock\n<!-- @id: stale-lock -->\n\nArchived: see `archive/2026-05-stale-lock.md`.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q (%s/%s), want staged (archive_section forces stage)",
			resp.Status, resp.Reason, resp.Message)
	}
	if !strings.Contains(resp.Routing.Reason, "forced to stage") {
		t.Errorf("routing reason missing force-stage note: %q", resp.Routing.Reason)
	}

	// Both files present in the staging dir.
	stageFiles := filepath.Join(memDir, "staging", resp.StagingID, "files")
	srcStaged, err := os.ReadFile(filepath.Join(stageFiles, "pitfalls.md"))
	if err != nil {
		t.Fatalf("staged source missing: %v", err)
	}
	if !strings.Contains(string(srcStaged), "Archived: see `archive/2026-05-stale-lock.md`") {
		t.Errorf("staged source not rewritten to stub:\n%s", srcStaged)
	}
	if strings.Contains(string(srcStaged), "Watch out.") {
		t.Errorf("original body still in staged source:\n%s", srcStaged)
	}
	archStaged, err := os.ReadFile(filepath.Join(stageFiles, "archive", "2026-05-stale-lock.md"))
	if err != nil {
		t.Fatalf("staged archive file missing: %v", err)
	}
	if !strings.Contains(string(archStaged), "Watch out.") {
		t.Errorf("archive didn't capture original body:\n%s", archStaged)
	}
}

// TestArchiveSection_RoundTripApply — stage then apply; both files land
// on disk with the right content.
func TestArchiveSection_RoundTripApply(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentArchiveStale,
			Operations: []OperationInput{
				{
					Op:          "archive_section",
					Path:        "pitfalls.md",
					SectionID:   "stale-lock",
					ArchivePath: "archive/legacy.md",
					Replacement: "## Stale Lock\n<!-- @id: stale-lock -->\n\nArchived.\n",
				},
			},
		}, deps)
	if err != nil || resp.Status != StatusStaged {
		t.Fatalf("stage failed: %v (%s/%s)", err, resp.Status, resp.Reason)
	}

	applyRes, err := ApplyStaged(context.Background(), resp.StagingID, deps)
	if err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	if applyRes.Status != StatusApplied {
		t.Fatalf("apply Status = %q (%s)", applyRes.Status, applyRes.Reason)
	}

	// Source rewritten on disk.
	src, _ := os.ReadFile(filepath.Join(memDir, "pitfalls.md"))
	if !strings.Contains(string(src), "Archived.") || strings.Contains(string(src), "Watch out.") {
		t.Errorf("source not archived on disk:\n%s", src)
	}
	// Archive file created with original content.
	arch, err := os.ReadFile(filepath.Join(memDir, "archive", "legacy.md"))
	if err != nil {
		t.Fatalf("archive file not on disk after apply: %v", err)
	}
	if !strings.Contains(string(arch), "Watch out.") {
		t.Errorf("archive content wrong:\n%s", arch)
	}
}

// TestProposeUpdate_ArchiveExists_Rejected — if the archive destination
// already exists on disk, the proposal is rejected (write-once).
func TestProposeUpdate_ArchiveExists_Rejected(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// Pre-create the archive destination.
	if err := os.WriteFile(filepath.Join(memDir, "archive", "taken.md"),
		[]byte("# Already here\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentArchiveStale,
			Operations: []OperationInput{
				{
					Op:          "archive_section",
					Path:        "pitfalls.md",
					SectionID:   "stale-lock",
					ArchivePath: "archive/taken.md",
					Replacement: "## Stale Lock\n<!-- @id: stale-lock -->\n\nstub.\n",
				},
			},
		}, deps)
	if resp.Reason != ReasonArchiveExists {
		t.Errorf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonArchiveExists, resp.Message)
	}
}

// =============================================================================
// remove_section end-to-end
// =============================================================================

func TestProposeUpdate_RemoveSection_ArchivesAndSplices(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentArchiveStale,
			Operations: []OperationInput{
				{
					Op:          "remove_section",
					Path:        "pitfalls.md",
					SectionID:   "stale-lock",
					ArchivePath: "archive/removed.md",
					Reason:      "no longer relevant",
				},
			},
		}, deps)
	if err != nil || resp.Status != StatusStaged {
		t.Fatalf("stage failed: %v (%s/%s)", err, resp.Status, resp.Message)
	}

	applyRes, err := ApplyStaged(context.Background(), resp.StagingID, deps)
	if err != nil {
		t.Fatal(err)
	}
	if applyRes.Status != StatusApplied {
		t.Fatalf("apply Status = %q (%s)", applyRes.Status, applyRes.Reason)
	}

	src, _ := os.ReadFile(filepath.Join(memDir, "pitfalls.md"))
	if strings.Contains(string(src), "Stale Lock") {
		t.Errorf("section not removed from source:\n%s", src)
	}
	arch, err := os.ReadFile(filepath.Join(memDir, "archive", "removed.md"))
	if err != nil {
		t.Fatalf("archive not created: %v", err)
	}
	if !strings.Contains(string(arch), "no longer relevant") {
		t.Errorf("archive missing reason:\n%s", arch)
	}
	if !strings.Contains(string(arch), "Watch out.") {
		t.Errorf("archive missing original body:\n%s", arch)
	}
}

// =============================================================================
// write-once enforcement
// =============================================================================

func TestProposeUpdate_WriteOnceViolation(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// An existing archive file.
	if err := os.WriteFile(filepath.Join(memDir, "archive", "frozen.md"),
		[]byte("# Frozen\n<!-- @id: frozen -->\n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Attempt to mutate it directly — must be rejected as write-once.
	resp, _ := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentArchiveStale,
			Operations: []OperationInput{
				{
					Op:        "replace_section_content",
					Path:      "archive/frozen.md",
					SectionID: "frozen",
					Content:   "rewritten body\n",
				},
			},
		}, deps)
	if resp.Reason != ReasonWriteOnceViolation {
		t.Errorf("Reason = %q, want %q (msg=%s)", resp.Reason, ReasonWriteOnceViolation, resp.Message)
	}
}

// =============================================================================
// rename_heading end-to-end (through stage path)
// =============================================================================

func TestProposeUpdate_RenameHeading_StagedFilePreservesAnchor(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// add_pitfall + non-append routes to pitfalls_replace → stage.
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent: IntentAddPitfall,
			Operations: []OperationInput{
				{
					Op:         "rename_heading",
					Path:       "pitfalls.md",
					SectionID:  "stale-lock",
					NewHeading: "Lock Contention",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q (%s/%s)", resp.Status, resp.Reason, resp.Message)
	}

	staged, err := os.ReadFile(filepath.Join(memDir, "staging", resp.StagingID, "files", "pitfalls.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(staged)
	if !strings.Contains(s, "## Lock Contention") {
		t.Errorf("heading not renamed in staged file:\n%s", s)
	}
	if !strings.Contains(s, "<!-- @id: stale-lock -->") {
		t.Errorf("anchor lost on rename:\n%s", s)
	}
	if !strings.Contains(s, "Watch out.") {
		t.Errorf("body disturbed on rename:\n%s", s)
	}
}
