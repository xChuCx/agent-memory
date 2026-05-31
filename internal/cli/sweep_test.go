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
	"time"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// sweepFixture inits .agent-memory/, stages two proposals (one young,
// one backdated past the manifest TTL), and returns the project root +
// both staging IDs.
func sweepFixture(t *testing.T) (root, oldID, youngID string) {
	t.Helper()
	root = t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: root, ProjectName: "sweep-cli"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	memDir := filepath.Join(root, ".agent-memory")

	mf, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Force a short TTL so the test doesn't depend on the manifest default.
	mf.Staging.TTLSeconds = 3600 // 1h
	if err := config.WriteManifest(filepath.Join(memDir, "meta", "manifest.yaml"), mf); err != nil {
		t.Fatal(err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	deps := memory.UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}
	mkProposal := func(rationale string) string {
		t.Helper()
		resp, err := memory.ProposeUpdate(context.Background(),
			memory.ProposeRequest{
				Intent:    memory.IntentRecordDecision,
				Rationale: rationale,
				Sources:   []memory.Source{{Type: "user", Ref: "sweep-cli"}},
				Operations: []memory.OperationInput{
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
			t.Fatal(err)
		}
		if resp.Status != memory.StatusStaged {
			t.Fatalf("Status = %q, want staged", resp.Status)
		}
		return resp.StagingID
	}
	oldID = mkProposal("old")
	youngID = mkProposal("young")

	// Backdate the "old" one by 2h so it's past the 1h TTL.
	backdateProposal(t, memDir, oldID, 2*time.Hour)
	return root, oldID, youngID
}

func backdateProposal(t *testing.T, memDir, id string, offset time.Duration) {
	t.Helper()
	p := filepath.Join(memDir, "staging", id, "proposal.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var env memory.StagedProposal
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	env.StagedAt = time.Now().UTC().Add(-offset).Format(time.RFC3339)
	out, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(p, out, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestRunSweep_RemovesOldKeepsYoung(t *testing.T) {
	root, oldID, youngID := sweepFixture(t)
	memDir := filepath.Join(root, ".agent-memory")

	res, err := runSweep(root, 0, false)
	if err != nil {
		t.Fatalf("runSweep: %v", err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != oldID {
		t.Errorf("Removed = %v, want [%s]", res.Removed, oldID)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", oldID)); err == nil {
		t.Errorf("old staging dir still present")
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", youngID)); err != nil {
		t.Errorf("young staging dir was removed unexpectedly: %v", err)
	}
}

func TestRunSweep_DryRunListsButDoesNotRemove(t *testing.T) {
	root, oldID, _ := sweepFixture(t)
	memDir := filepath.Join(root, ".agent-memory")

	res, err := runSweep(root, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || len(res.Expired) != 1 || len(res.Removed) != 0 {
		t.Errorf("dry-run result wrong: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(memDir, "staging", oldID)); err != nil {
		t.Errorf("dry-run removed the dir: %v", err)
	}
}

func TestRunSweep_TTLOverride(t *testing.T) {
	root, oldID, youngID := sweepFixture(t)
	// Override with 24h — even the "old" one (2h) should NOT match.
	res, err := runSweep(root, 24*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Expired) != 0 {
		t.Errorf("Expired = %+v, want empty under 24h TTL", res.Expired)
	}
	memDir := filepath.Join(root, ".agent-memory")
	for _, id := range []string{oldID, youngID} {
		if _, err := os.Stat(filepath.Join(memDir, "staging", id)); err != nil {
			t.Errorf("staging dir %s removed under permissive --ttl: %v", id, err)
		}
	}
}

func TestCobra_SweepHumanOutput(t *testing.T) {
	root, oldID, _ := sweepFixture(t)

	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"sweep", "--root", root})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sweep: %v\n%s", err, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, oldID) {
		t.Errorf("stdout missing oldID %s: %q", oldID, out)
	}
	if !strings.Contains(out, "Removed 1") {
		t.Errorf("stdout missing 'Removed 1' banner: %q", out)
	}
}

func TestCobra_SweepJSONOutput(t *testing.T) {
	root, oldID, _ := sweepFixture(t)

	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"sweep", "--root", root, "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var res memory.SweepResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout.String())
	}
	if len(res.Removed) != 1 || res.Removed[0] != oldID {
		t.Errorf("Removed = %v, want [%s]", res.Removed, oldID)
	}
}

func TestRunSweep_EmptyStagingDir(t *testing.T) {
	root := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: root, ProjectName: "empty"}); err != nil {
		t.Fatal(err)
	}
	res, err := runSweep(root, 0, false)
	if err != nil {
		t.Fatalf("runSweep on empty staging errored: %v", err)
	}
	if len(res.Expired) != 0 || len(res.Removed) != 0 {
		t.Errorf("Expected empty result, got %+v", res)
	}
}
