package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// =============================================================================
// shouldStage: per-file policy logic
// =============================================================================

func TestShouldStage_TrackedCategoryGoesIn(t *testing.T) {
	sch := schema.DefaultSchema()
	// decisions.md → GitTracked: true in defaults.
	if !shouldStage("decisions.md", sch, config.Git{}) {
		t.Error("decisions.md should be staged (GitTracked=true)")
	}
}

func TestShouldStage_UntrackedCategoryDefaultsToSkip(t *testing.T) {
	sch := schema.DefaultSchema()
	// local/current.shared.md → GitTracked: false in defaults.
	if shouldStage("local/current.shared.md", sch, config.Git{}) {
		t.Error("local/* should be skipped by default (GitTracked=false)")
	}
	if shouldStage("sessions/2026-05-27.md", sch, config.Git{}) {
		t.Error("sessions/* should be skipped by default")
	}
}

func TestShouldStage_TrackLocalOverride(t *testing.T) {
	sch := schema.DefaultSchema()
	if !shouldStage("local/current.shared.md", sch, config.Git{TrackLocal: true}) {
		t.Error("local/* should be staged when TrackLocal=true")
	}
	// Doesn't accidentally trigger for sessions/.
	if shouldStage("sessions/2026-05-27.md", sch, config.Git{TrackLocal: true}) {
		t.Error("TrackLocal must not also stage sessions/*")
	}
}

func TestShouldStage_TrackSessionsOverride(t *testing.T) {
	sch := schema.DefaultSchema()
	if !shouldStage("sessions/2026-05-27.md", sch, config.Git{TrackSessions: true}) {
		t.Error("sessions/* should be staged when TrackSessions=true")
	}
}

func TestShouldStage_UnknownPathSkipped(t *testing.T) {
	sch := schema.DefaultSchema()
	// No category matches "random/elsewhere.md" → skip even with both
	// overrides flipped on.
	if shouldStage("random/elsewhere.md", sch,
		config.Git{TrackLocal: true, TrackSessions: true}) {
		t.Error("unknown path should be skipped")
	}
}

func TestShouldStage_NilSchema(t *testing.T) {
	if shouldStage("decisions.md", nil, config.Git{}) {
		t.Error("nil schema should never stage")
	}
}

// =============================================================================
// composeCommitMessage
// =============================================================================

func TestComposeCommitMessage_WithRationale(t *testing.T) {
	got := composeCommitMessage(
		"chore(memory):",
		IntentRecordDecision,
		"use postgres",
		[]string{".agent-memory/decisions.md"},
	)
	wantTitle := "chore(memory): record_decision — use postgres"
	if !strings.HasPrefix(got, wantTitle+"\n\n") {
		t.Errorf("title = %q, want prefix %q", got, wantTitle)
	}
	if !strings.Contains(got, "Files: .agent-memory/decisions.md") {
		t.Errorf("body missing Files line: %q", got)
	}
}

func TestComposeCommitMessage_NoRationale(t *testing.T) {
	got := composeCommitMessage(
		"chore(memory):",
		IntentSessionLog,
		"",
		[]string{".agent-memory/sessions/2026-05-27.md"},
	)
	if !strings.HasPrefix(got, "chore(memory): session_log\n\n") {
		t.Errorf("no-rationale title wrong: %q", got)
	}
}

func TestComposeCommitMessage_DefaultPrefix(t *testing.T) {
	// Empty prefix falls back to "chore(memory):".
	got := composeCommitMessage("", IntentUpdateCurrent, "wip", []string{"x"})
	if !strings.HasPrefix(got, "chore(memory): update_current — wip") {
		t.Errorf("default prefix wrong: %q", got)
	}
}

func TestComposeCommitMessage_NoFiles(t *testing.T) {
	got := composeCommitMessage("chore(memory):", IntentAddPitfall, "lock retry", nil)
	if strings.Contains(got, "Files:") {
		t.Errorf("body should have no Files line when staged is empty: %q", got)
	}
}

// =============================================================================
// maybeAutoStage end-to-end against a real git repo
// =============================================================================

// gitRepoFixture sets up:
//   <tmp>/
//     .git/                 (real repo, initial commit)
//     .agent-memory/        (with seed files via updateFixture-like setup)
//
// Returns the absolute project root and an UpdateDeps wired with manifest
// + schema where Git.AutoStageChanges is true (caller adjusts as needed).
func gitRepoFixture(t *testing.T) (root string, deps UpdateDeps) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}

	root = t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	mustGit("init", "-q")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "Test")
	mustGit("commit", "--allow-empty", "-m", "init", "-q")

	// Build .agent-memory/ under this git root.
	memDir := filepath.Join(root, ".agent-memory")
	mf, sch := makeMemoryDir(t, memDir)
	mf.Git.AutoStageChanges = true
	mf.Git.AutoCommit = true
	deps = UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
	return root, deps
}

// makeMemoryDir replicates the minimal .agent-memory/ tree from
// updateFixture without depending on its t.TempDir() — caller supplies
// memDir under their own root.
func makeMemoryDir(t *testing.T, memDir string) (*config.Manifest, *schema.Schema) {
	t.Helper()
	for _, sub := range []string{"meta", "local", "sessions", "modules", "archive"} {
		mustMkdir(t, filepath.Join(memDir, sub))
	}
	mustWrite(t, filepath.Join(memDir, "decisions.md"), []byte("# Decisions\n"))
	mustWrite(t, filepath.Join(memDir, "pitfalls.md"),
		[]byte("# Pitfalls\n\n## Stale\n<!-- @id: stale -->\n\nx.\n"))
	mustWrite(t, filepath.Join(memDir, "conventions.md"), []byte("# Conventions\n"))
	mustWrite(t, filepath.Join(memDir, "local", "current.shared.md"), []byte("# Current\n"))
	return config.DefaultManifest(), schema.DefaultSchema()
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
}

// headSubject returns the first line of HEAD's commit message in root.
// Local to this file to avoid cross-package imports of test helpers.
func headSubject(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// Verify auto-stage actually adds tracked files to the git index and
// creates a commit when AutoCommit is on.
func TestMaybeAutoStage_StagesAndCommits(t *testing.T) {
	root, deps := gitRepoFixture(t)

	res := maybeAutoStage(deps, root, []string{"decisions.md"},
		IntentRecordDecision, "smoke test")
	if res.Skipped {
		t.Fatalf("Skipped=true unexpectedly: %+v", res)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors not empty: %v", res.Errors)
	}
	if len(res.Staged) != 1 {
		t.Errorf("Staged = %v, want one entry", res.Staged)
	}
	if res.CommitSHA == "" {
		t.Error("CommitSHA empty after AutoCommit=true")
	}

	// HEAD subject contains intent + rationale.
	subj := headSubject(t, root)
	if !strings.Contains(subj, "record_decision — smoke test") {
		t.Errorf("HEAD subject = %q, want intent + rationale", subj)
	}
}

func TestMaybeAutoStage_DisabledIsNoop(t *testing.T) {
	root, deps := gitRepoFixture(t)
	deps.Manifest.Git.AutoStageChanges = false

	res := maybeAutoStage(deps, root, []string{"decisions.md"},
		IntentRecordDecision, "irrelevant")
	if !res.Skipped {
		t.Errorf("Skipped should be true when AutoStageChanges=false; got %+v", res)
	}
	if len(res.Staged) != 0 || res.CommitSHA != "" {
		t.Errorf("unexpected side effects with feature disabled: %+v", res)
	}
}

func TestMaybeAutoStage_AutoCommitOffStagesButNoCommit(t *testing.T) {
	root, deps := gitRepoFixture(t)
	deps.Manifest.Git.AutoCommit = false

	res := maybeAutoStage(deps, root, []string{"decisions.md"},
		IntentRecordDecision, "no-commit path")
	if len(res.Staged) != 1 {
		t.Errorf("Staged = %v, want one entry", res.Staged)
	}
	if res.CommitSHA != "" {
		t.Errorf("CommitSHA = %q, want empty when AutoCommit=false", res.CommitSHA)
	}
	// HEAD subject must still be the initial "init" commit.
	if subj := headSubject(t, root); subj != "init" {
		t.Errorf("HEAD subject = %q, want init (no commit should have happened)", subj)
	}
}

func TestMaybeAutoStage_SkipsUntrackedFiles(t *testing.T) {
	root, deps := gitRepoFixture(t)
	// Don't flip TrackLocal — local/current.shared.md should be skipped.
	res := maybeAutoStage(deps, root, []string{"local/current.shared.md"},
		IntentUpdateCurrent, "local notes")
	if !res.Skipped {
		t.Errorf("untracked-only file set should produce Skipped=true; got %+v", res)
	}
}

func TestMaybeAutoStage_TrackLocalEnablesLocalStaging(t *testing.T) {
	root, deps := gitRepoFixture(t)
	deps.Manifest.Git.TrackLocal = true
	res := maybeAutoStage(deps, root, []string{"local/current.shared.md"},
		IntentUpdateCurrent, "shared notes")
	if res.Skipped {
		t.Errorf("Skipped=true with TrackLocal on: %+v", res)
	}
	if len(res.Staged) != 1 {
		t.Errorf("Staged = %v, want one entry", res.Staged)
	}
}

func TestMaybeAutoStage_NotAGitRepoSkipped(t *testing.T) {
	// No git init under root.
	root := t.TempDir()
	memDir := filepath.Join(root, ".agent-memory")
	mf, sch := makeMemoryDir(t, memDir)
	mf.Git.AutoStageChanges = true
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	res := maybeAutoStage(deps, root, []string{"decisions.md"},
		IntentRecordDecision, "test")
	// Non-git is silent: AddPaths returns (nil, nil), len(staged)==0
	// → Skipped after the loop.
	if len(res.Errors) != 0 {
		t.Errorf("non-git project should not produce errors: %v", res.Errors)
	}
	if res.CommitSHA != "" {
		t.Errorf("non-git project should not produce a commit: %s", res.CommitSHA)
	}
}

// =============================================================================
// Integration: full ProposeUpdate / ApplyStaged flow through git auto-stage
// =============================================================================

// TestProposeUpdate_AppliesAndAutoStages — the apply path (apply intent like
// add_pitfall via append_to_section) writes a file AND auto-stages +
// commits it when the manifest has the flags on.
func TestProposeUpdate_AppliesAndAutoStages(t *testing.T) {
	root, deps := gitRepoFixture(t)

	// add_pitfall + append_to_section routes to apply per default manifest.
	resp, err := ProposeUpdate(t.Context(),
		ProposeRequest{
			Intent:    IntentAddPitfall,
			Rationale: "lock retry pattern",
			Operations: []OperationInput{
				{
					Op:        "append_to_section",
					Path:      "pitfalls.md",
					SectionID: "stale",
					Content:   "- New bullet from autostage integration test.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != StatusApplied {
		t.Fatalf("Status = %q (%s/%s), want applied", resp.Status, resp.Reason, resp.Message)
	}
	if resp.AutoStage == nil {
		t.Fatal("AutoStage missing on applied response")
	}
	if resp.AutoStage.Skipped {
		t.Errorf("AutoStage.Skipped=true unexpectedly: %+v", resp.AutoStage)
	}
	// The applied edit touches pitfalls.md, and the server regenerates
	// the durable index.md as a side effect — both git-tracked, so both
	// land in the auto-stage batch.
	staged := map[string]bool{}
	for _, s := range resp.AutoStage.Staged {
		staged[s] = true
	}
	if !staged[".agent-memory/pitfalls.md"] {
		t.Errorf("AutoStage.Staged missing pitfalls.md: %v", resp.AutoStage.Staged)
	}
	if !staged[".agent-memory/index.md"] {
		t.Errorf("AutoStage.Staged missing regenerated index.md: %v", resp.AutoStage.Staged)
	}
	if resp.AutoStage.CommitSHA == "" {
		t.Error("AutoStage.CommitSHA empty after AutoCommit=true")
	}
	if subj := headSubject(t, root); !strings.Contains(subj, "add_pitfall — lock retry pattern") {
		t.Errorf("HEAD subject = %q, missing intent+rationale", subj)
	}
}

// TestApplyStaged_AutoStagesOnApply — the staged-apply path also runs
// auto-stage. Stage a decision (which routes to stage per default
// manifest), then ApplyStaged, then verify the response carries an
// AutoStage with a CommitSHA and HEAD reflects the staged proposal.
func TestApplyStaged_AutoStagesOnApply(t *testing.T) {
	root, deps := gitRepoFixture(t)

	// First propose with auto-stage temporarily disabled so the stage
	// path doesn't try to git-add staging artefacts. We re-enable below
	// for the apply step.
	deps.Manifest.Git.AutoStageChanges = false
	stageResp, err := ProposeUpdate(t.Context(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: "stage then apply",
			Sources:   []Source{{Type: "user", Ref: "test"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Auto-stage",
					HeadingLevel: 2,
					Content:      "## Auto-stage\n<!-- @id: auto-stage -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nDecided.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate (stage): %v", err)
	}
	if stageResp.Status != StatusStaged {
		t.Fatalf("expected staged, got %s", stageResp.Status)
	}

	// Now re-enable auto-stage and apply.
	deps.Manifest.Git.AutoStageChanges = true
	applyRes, err := ApplyStaged(t.Context(), stageResp.StagingID, deps)
	if err != nil {
		t.Fatalf("ApplyStaged: %v", err)
	}
	if applyRes.Status != StatusApplied {
		t.Fatalf("apply Status = %q (%s)", applyRes.Status, applyRes.Reason)
	}
	if applyRes.AutoStage == nil {
		t.Fatal("ApplyResult.AutoStage missing")
	}
	if applyRes.AutoStage.CommitSHA == "" {
		t.Error("AutoStage.CommitSHA empty after AutoCommit=true on apply")
	}
	if subj := headSubject(t, root); !strings.Contains(subj, "record_decision — stage then apply") {
		t.Errorf("HEAD subject = %q, want commit message from staged Request", subj)
	}
}
