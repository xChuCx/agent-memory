package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// stagingFixture inits an .agent-memory/ tree and stages a record_decision
// proposal so review/apply/reject have something to operate on. Returns the
// repo root and the staging id.
func stagingFixture(t *testing.T) (root, stagingID string) {
	t.Helper()
	root = t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: root, ProjectName: "staging-cli-test"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	memDir := filepath.Join(root, ".agent-memory")

	mf, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := memory.ProposeUpdate(context.Background(),
		memory.ProposeRequest{
			Intent:    memory.IntentRecordDecision,
			Rationale: "cli test decision",
			Sources:   []memory.Source{{Type: "user", Ref: "test"}},
			Operations: []memory.OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "CLI Test",
					HeadingLevel: 2,
					Content:      "## CLI Test\n<!-- @id: cli-test -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nDecided.\n",
				},
			},
		}, memory.UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir})
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != memory.StatusStaged {
		t.Fatalf("expected staged, got %s (%s/%s)", resp.Status, resp.Reason, resp.Message)
	}
	return root, resp.StagingID
}

// =============================================================================
// review
// =============================================================================

func TestRunReviewList_FindsStagedProposal(t *testing.T) {
	root, id := stagingFixture(t)
	list, err := runReviewList(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Proposals) != 1 {
		t.Fatalf("Proposals len = %d, want 1", len(list.Proposals))
	}
	if list.Proposals[0].StagingID != id {
		t.Errorf("StagingID = %q, want %q", list.Proposals[0].StagingID, id)
	}
}

func TestRunReviewList_NoStaged(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "empty"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	list, err := runReviewList(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Proposals) != 0 {
		t.Errorf("expected empty list, got %d", len(list.Proposals))
	}
}

func TestRunReviewDetail_ReturnsTargetsAndContent(t *testing.T) {
	root, id := stagingFixture(t)
	d, err := runReviewDetail(root, id, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if d.Proposal.StagingID != id {
		t.Errorf("StagingID = %q, want %q", d.Proposal.StagingID, id)
	}
	if len(d.Targets) == 0 {
		t.Error("expected at least one target")
	}
	if d.Files == nil {
		t.Fatal("Files should be populated with --show")
	}
	body, ok := d.Files["decisions.md"]
	if !ok {
		t.Fatalf("decisions.md missing from Files map: %v", d.Files)
	}
	if !strings.Contains(body, "cli-test") {
		t.Errorf("staged file content missing staged section: %q", body)
	}
}

func TestRunReviewDetail_UnknownID(t *testing.T) {
	root, _ := stagingFixture(t)
	_, err := runReviewDetail(root, "no-such-id", false, false)
	if err == nil {
		t.Error("expected error for unknown staging id")
	}
}

// =============================================================================
// apply
// =============================================================================

func TestRunApply_HappyPath(t *testing.T) {
	root, id := stagingFixture(t)
	memDir := filepath.Join(root, ".agent-memory")

	// Pre-condition: staged section is NOT in decisions.md.
	before, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if strings.Contains(string(before), "cli-test") {
		t.Fatal("test precondition violated")
	}

	res, err := runApply(context.Background(), root, id)
	if err != nil {
		t.Fatalf("runApply: %v", err)
	}
	if res.Status != memory.StatusApplied {
		t.Fatalf("Status = %q (%s/%s)", res.Status, res.Reason, res.Message)
	}

	after, _ := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if !strings.Contains(string(after), "cli-test") {
		t.Errorf("decision not applied: %q", after)
	}
	// Staging dir gone.
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err == nil {
		t.Errorf("staging dir still present after apply")
	}
}

func TestRunApply_UnknownIDIsRejection(t *testing.T) {
	root, _ := stagingFixture(t)
	res, err := runApply(context.Background(), root, "no-such-id")
	if err != nil {
		t.Fatalf("unknown id should not error at Go level: %v", err)
	}
	if res.Reason != memory.ReasonStagingNotFound {
		t.Errorf("Reason = %q, want %q", res.Reason, memory.ReasonStagingNotFound)
	}
}

// =============================================================================
// reject
// =============================================================================

func TestRunReject_RemovesDir(t *testing.T) {
	root, id := stagingFixture(t)
	memDir := filepath.Join(root, ".agent-memory")

	res, err := runReject(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Status, "rejected") {
		t.Errorf("Status = %q, want a rejected_* form", res.Status)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err == nil {
		t.Errorf("staging dir still present after reject")
	}
}

func TestRunReject_UnknownIDIsNonError(t *testing.T) {
	root, _ := stagingFixture(t)
	res, err := runReject(root, "no-such-id")
	if err != nil {
		t.Fatalf("unknown id should not be a Go error: %v", err)
	}
	if res.Reason != memory.ReasonStagingNotFound {
		t.Errorf("Reason = %q, want %q", res.Reason, memory.ReasonStagingNotFound)
	}
}

// =============================================================================
// Cobra integration: end-to-end through NewRootCmd
// =============================================================================

func TestCobra_ApplyUnknownIDExitsNonZero(t *testing.T) {
	// An unknown id is now caught at staging-id RESOLUTION (a prefix that
	// matches nothing → ErrNoStaged) before the apply runs, so the command
	// exits non-zero with a resolution error rather than a drift banner.
	root, _ := stagingFixture(t)

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"apply", "--root", root, "no-such-id-prefix-zzz"})

	err := cmd.Execute()
	if err == nil {
		t.Errorf("expected non-zero exit for unknown id, got nil. stdout=%q", stdout.String())
	}
	if err != nil && !strings.Contains(err.Error(), "no matching staged proposal") {
		t.Errorf("err = %v, want mention of 'no matching staged proposal'", err)
	}
}

func TestCobra_ApplyByPrefix(t *testing.T) {
	root, id := stagingFixture(t)
	// Use a 12-char prefix of the staging id (timestamp portion).
	prefix := id[:12]

	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"apply", "--root", root, prefix})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("apply by prefix %q: %v\n%s", prefix, err, stdout.String())
	}
	// Staging dir gone → applied.
	if _, err := os.Stat(filepath.Join(root, ".agent-memory", "staging", id)); err == nil {
		t.Errorf("staging dir still present after apply-by-prefix")
	}
}

func TestCobra_ApplyLatest(t *testing.T) {
	root, id := stagingFixture(t)
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"apply", "--root", root, "--latest"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("apply --latest: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".agent-memory", "staging", id)); err == nil {
		t.Errorf("staging dir still present after apply --latest")
	}
}

func TestCobra_RejectByPrefix(t *testing.T) {
	root, id := stagingFixture(t)
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"reject", "--root", root, id[:12]})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("reject by prefix: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".agent-memory", "staging", id)); err == nil {
		t.Errorf("staging dir still present after reject-by-prefix")
	}
}

func TestCobra_ReviewLatest(t *testing.T) {
	root, id := stagingFixture(t)
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"review", "--root", root, "--latest", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("review --latest: %v\n%s", err, stdout.String())
	}
	var d ReviewDetail
	if err := json.Unmarshal(stdout.Bytes(), &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout.String())
	}
	if d.Proposal == nil || d.Proposal.StagingID != id {
		t.Errorf("review --latest didn't resolve to %q: %+v", id, d.Proposal)
	}
}

func TestCobra_ReviewListJSON(t *testing.T) {
	root, id := stagingFixture(t)
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"review", "--root", root, "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("review --json: %v (stderr=%q)", err, stderr.String())
	}
	var got ReviewList
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Proposals) != 1 || got.Proposals[0].StagingID != id {
		t.Errorf("got = %+v, want one proposal with id %q", got, id)
	}
}
