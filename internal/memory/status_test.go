package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentgit "github.com/xChuCx/agent-memory/internal/git"
)

// statusFixture builds a fresh updateFixture and returns it along with
// a default StatusDeps. Tests then mutate the tree and re-call
// BuildStatus.
func statusFixture(t *testing.T) (memDir string, deps StatusDeps) {
	t.Helper()
	memDir, mf, sch := updateFixture(t)
	deps = StatusDeps{
		MemoryDir:     memDir,
		Manifest:      mf,
		Schema:        sch,
		MemoryVersion: "test",
	}
	return memDir, deps
}

// =============================================================================
// Required-fields contract
// =============================================================================

func TestBuildStatus_PopulatesAllRequiredFields(t *testing.T) {
	_, deps := statusFixture(t)
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatalf("BuildStatus: %v", err)
	}
	if st.MemoryVersion != "test" {
		t.Errorf("MemoryVersion = %q, want test", st.MemoryVersion)
	}
	// Default fixture has decisions, pitfalls, conventions, plus an
	// empty local/current.shared.md. No archive/sessions/staging
	// content yet.
	if st.DurableFiles < 3 {
		t.Errorf("DurableFiles = %d, want >= 3", st.DurableFiles)
	}
	if st.ArchiveFiles != 0 {
		t.Errorf("ArchiveFiles = %d, want 0 on fresh fixture", st.ArchiveFiles)
	}
	if st.LocalCurrentFiles != 1 {
		t.Errorf("LocalCurrentFiles = %d, want 1 (current.shared.md)", st.LocalCurrentFiles)
	}
	if st.Security.LastSecretScan != "n/a" {
		t.Errorf("LastSecretScan = %q, want 'n/a' on first cut", st.Security.LastSecretScan)
	}
}

// =============================================================================
// File counting
// =============================================================================

func TestBuildStatus_CountsArchiveAndSessions(t *testing.T) {
	memDir, deps := statusFixture(t)
	// Add archive + session files.
	must := func(rel, body string) {
		t.Helper()
		path := filepath.Join(memDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	must("archive/2026-05-old.md", "# Archived\n")
	must("archive/2026-04-older.md", "# Archived\n")
	must("sessions/2026-05-27.md", "# Today\n")

	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.ArchiveFiles != 2 {
		t.Errorf("ArchiveFiles = %d, want 2", st.ArchiveFiles)
	}
	if st.LocalSessions != 1 {
		t.Errorf("LocalSessions = %d, want 1", st.LocalSessions)
	}
}

// =============================================================================
// Sizes
// =============================================================================

func TestBuildStatus_ComputesSizes(t *testing.T) {
	memDir, deps := statusFixture(t)
	// Force-create a fake index file so IndexSizeBytes is non-zero.
	if err := os.WriteFile(filepath.Join(memDir, "meta", "index.sqlite"),
		[]byte(strings.Repeat("x", 1024)), 0644); err != nil {
		t.Fatal(err)
	}
	// Replace empty current.shared.md with non-empty content.
	if err := os.WriteFile(filepath.Join(memDir, "local", "current.shared.md"),
		[]byte("# Current\n\nactive work."), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.IndexSizeBytes != 1024 {
		t.Errorf("IndexSizeBytes = %d, want 1024", st.IndexSizeBytes)
	}
	if st.CurrentSizeBytes == 0 {
		t.Errorf("CurrentSizeBytes = 0, want >0 with content present")
	}
}

// =============================================================================
// Staged updates summary
// =============================================================================

func TestBuildStatus_StagedSummaryWithDriftCheck(t *testing.T) {
	memDir, deps := statusFixture(t)
	// Stage a proposal via ProposeUpdate so the status report has
	// something to summarise.
	updDeps := UpdateDeps{Manifest: deps.Manifest, Schema: deps.Schema, MemoryDir: memDir}
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: "status test",
			Sources:   []Source{{Type: "user", Ref: "test"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Status Test",
					HeadingLevel: 2,
					Content:      "## Status Test\n<!-- @id: status-test -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nDecided.\n",
				},
			},
		}, updDeps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("Status = %q, want staged", resp.Status)
	}

	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.StagedUpdates) != 1 {
		t.Fatalf("StagedUpdates len = %d, want 1", len(st.StagedUpdates))
	}
	su := st.StagedUpdates[0]
	if su.ID != resp.StagingID {
		t.Errorf("ID = %q, want %q", su.ID, resp.StagingID)
	}
	if su.Intent != string(IntentRecordDecision) {
		t.Errorf("Intent = %q, want record_decision", su.Intent)
	}
	if su.TTLRemainingSeconds <= 0 {
		t.Errorf("TTLRemainingSeconds = %d, want >0 (manifest default 604800)", su.TTLRemainingSeconds)
	}
	if su.DriftDetected {
		t.Errorf("DriftDetected = true on a freshly-staged proposal")
	}
	if len(su.TargetFiles) == 0 {
		t.Errorf("TargetFiles empty")
	}
}

// =============================================================================
// Allowlist counting
// =============================================================================

func TestBuildStatus_CountsAllowlistedRegions(t *testing.T) {
	memDir, deps := statusFixture(t)
	body := []byte("Prose.\n\n<!-- @secret-scan: allow reason=\"docs\" -->\nExample.\n<!-- @secret-scan: end -->\n")
	if err := os.WriteFile(filepath.Join(memDir, "conventions.md"), body, 0644); err != nil {
		t.Fatal(err)
	}
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.Security.AllowlistedRegions != 1 {
		t.Errorf("AllowlistedRegions = %d, want 1", st.Security.AllowlistedRegions)
	}
}

// =============================================================================
// .gitignore detection
// =============================================================================

func TestBuildStatus_DetectsIgnoredLocalState(t *testing.T) {
	memDir, deps := statusFixture(t)
	// updateFixture doesn't create .gitignore by default — write one
	// that excludes local/.
	if err := os.WriteFile(filepath.Join(memDir, ".gitignore"),
		[]byte("local/\nsessions/\nmeta/index.sqlite*\n"), 0644); err != nil {
		t.Fatal(err)
	}
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Git.IgnoredLocalState {
		t.Errorf("IgnoredLocalState = false, want true")
	}
}

func TestBuildStatus_MissingGitignoreIsNotIgnored(t *testing.T) {
	_, deps := statusFixture(t)
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.Git.IgnoredLocalState {
		t.Errorf("IgnoredLocalState = true without .gitignore present")
	}
}

// =============================================================================
// Manifest passthrough
// =============================================================================

func TestBuildStatus_MirrorsManifestGitFlags(t *testing.T) {
	_, deps := statusFixture(t)
	deps.Manifest.Git.TrackLocal = true
	deps.Manifest.Git.TrackSessions = true
	deps.Manifest.Git.MergeDriverInstalled = true
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Git.TrackLocal || !st.Git.TrackSessions || !st.Git.MergeDriverInstalled {
		t.Errorf("Git block didn't mirror manifest flags: %+v", st.Git)
	}
}

// =============================================================================
// Branch active info
// =============================================================================

func TestBuildStatus_ReflectsActiveBranch(t *testing.T) {
	_, deps := statusFixture(t)
	deps.Branch = agentgit.BranchInfo{Name: "feature/auth", IsGitRepo: true}
	st, err := BuildStatus(context.Background(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveBranch != "feature/auth" {
		t.Errorf("ActiveBranch = %q, want feature/auth", st.ActiveBranch)
	}
}

// =============================================================================
// Required fields validation
// =============================================================================

func TestBuildStatus_MissingDepsErrors(t *testing.T) {
	_, err := BuildStatus(context.Background(), StatusDeps{})
	if err == nil {
		t.Fatal("BuildStatus with empty deps should error")
	}
}
