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

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/memory"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// rebaseFixture inits .agent-memory/, stages an add_pitfall +
// replace_section_content (which routes to stage per default manifest's
// pitfalls_replace), and returns the project root + staging id.
//
// updateFixture / stageContentMatchProposal both live in the memory
// package's tests — we re-implement the minimum here to stay in the
// cli package.
func rebaseFixture(t *testing.T) (root, stagingID string) {
	t.Helper()
	root = t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: root, ProjectName: "rebase-cli"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	memDir := filepath.Join(root, ".agent-memory")

	// Seed pitfalls.md with a known section + anchor that we can target.
	pitfalls := filepath.Join(memDir, "pitfalls.md")
	if err := os.WriteFile(pitfalls,
		[]byte("# Pitfalls\n<!-- @id: pitfalls -->\n\n## Stale Lock\n<!-- @id: stale-lock -->\n\nOriginal body.\n"),
		0644); err != nil {
		t.Fatal(err)
	}

	mf, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	deps := memory.UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	resp, err := memory.ProposeUpdate(context.Background(),
		memory.ProposeRequest{
			Intent:    memory.IntentAddPitfall,
			Rationale: "rewrite for rebase test",
			Operations: []memory.OperationInput{
				{
					Op:        "replace_section_content",
					Path:      "pitfalls.md",
					SectionID: "stale-lock",
					Content:   "Rebased body content.\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatalf("ProposeUpdate: %v", err)
	}
	if resp.Status != memory.StatusStaged {
		t.Fatalf("Status = %q, want staged (%s/%s)", resp.Status, resp.Reason, resp.Message)
	}
	return root, resp.StagingID
}

// mutateBase rewrites pitfalls.md so the stale-lock section's body
// differs (soft drift triggered).
func mutateBase(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, ".agent-memory", "pitfalls.md")
	body := []byte("# Pitfalls\n<!-- @id: pitfalls -->\n\n## Stale Lock\n<!-- @id: stale-lock -->\n\nMutated body.\n")
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
}

// =============================================================================
// runRebase
// =============================================================================

func TestRunRebase_NoDriftSkippedClean(t *testing.T) {
	root, id := rebaseFixture(t)
	res, err := runRebase(context.Background(), root, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != memory.StatusSkippedClean {
		t.Errorf("Status = %q, want skipped_clean", res.Status)
	}
}

func TestRunRebase_SoftDriftNoForceRejected(t *testing.T) {
	root, id := rebaseFixture(t)
	mutateBase(t, root)
	res, err := runRebase(context.Background(), root, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != memory.ReasonForceRequired {
		t.Errorf("Reason = %q, want force_required", res.Reason)
	}
}

func TestRunRebase_SoftDriftForceSucceeds(t *testing.T) {
	root, id := rebaseFixture(t)
	mutateBase(t, root)
	res, err := runRebase(context.Background(), root, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != memory.StatusRebased {
		t.Fatalf("Status = %q (%s)", res.Status, res.Message)
	}
	// Staged file now reflects new base + proposal content.
	staged := filepath.Join(root, ".agent-memory", "staging", id, "files", "pitfalls.md")
	body, _ := os.ReadFile(staged)
	if !strings.Contains(string(body), "Rebased body content.") {
		t.Errorf("staged file missing replacement content:\n%s", body)
	}
}

func TestRunRebase_UnknownID(t *testing.T) {
	root, _ := rebaseFixture(t)
	res, err := runRebase(context.Background(), root, "no-such-id", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != memory.ReasonStagingNotFound {
		t.Errorf("Reason = %q, want %q", res.Reason, memory.ReasonStagingNotFound)
	}
}

// =============================================================================
// Cobra integration
// =============================================================================

func TestCobra_RebaseNonZeroExitOnRejection(t *testing.T) {
	root, id := rebaseFixture(t)
	mutateBase(t, root)

	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rebase", "--root", root, id}) // no --force

	if err := cmd.Execute(); err == nil {
		t.Errorf("expected non-zero exit on rejection; stdout=%q", stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "REJECTED") {
		t.Errorf("stdout missing REJECTED banner: %q", out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("stdout missing --force hint: %q", out)
	}
}

func TestCobra_RebaseJSON(t *testing.T) {
	root, id := rebaseFixture(t)
	mutateBase(t, root)

	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rebase", "--root", root, "--force", "--json", id})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("rebase --force --json: %v\n%s", err, stdout.String())
	}
	var res memory.RebaseResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout.String())
	}
	if res.Status != memory.StatusRebased {
		t.Errorf("Status = %q, want rebased", res.Status)
	}
}
