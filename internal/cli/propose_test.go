package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/memory"
)

func proposeInit(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: root, ProjectName: "propose-test"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return root
}

func readMem(t *testing.T, root, rel string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(root, ".agent-memory", filepath.FromSlash(rel)))
	return string(b)
}

// decisionContent is a schema-valid decisions.md section body (Date/Status/
// Confidence present) so tests exercise routing/provenance, not field errors.
const decisionContent = "## Use X\n<!-- @id: use-x -->\n\n**Date:** 2026-05-29\n**Status:** active\n**Confidence:** confirmed\n\nBecause reasons.\n"

func TestRunPropose_FlagAddPitfallApplies(t *testing.T) {
	root := proposeInit(t)
	f := &proposeFlags{
		root: root, intent: "add_pitfall", op: "append_to_section",
		path: "pitfalls.md", sectionID: "pitfalls",
		content: "- always lock A before B\n",
	}
	rep, err := runPropose(context.Background(), f, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != memory.StatusApplied {
		t.Fatalf("status = %s (%s/%s), want applied", rep.Status, rep.Reason, rep.Message)
	}
	if !strings.Contains(readMem(t, root, "pitfalls.md"), "lock A before B") {
		t.Error("bullet not written to pitfalls.md")
	}
}

func TestRunPropose_FlagRecordDecisionStages(t *testing.T) {
	root := proposeInit(t)
	f := &proposeFlags{
		root: root, intent: "record_decision", op: "append_section",
		path: "decisions.md", heading: "Use X", headingLevel: 2,
		content: decisionContent,
		sources: []string{"user:meeting-2026-05-29"}, confidence: "confirmed",
	}
	rep, err := runPropose(context.Background(), f, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != memory.StatusStaged {
		t.Fatalf("status = %s (%s), want staged", rep.Status, rep.Reason)
	}
	if rep.StagingID == "" {
		t.Error("empty StagingID on staged result")
	}
	if rep.Applied != nil {
		t.Error("Applied should be nil without --apply")
	}
	if strings.Contains(readMem(t, root, "decisions.md"), "use-x") {
		t.Error("decision leaked to disk on stage")
	}
}

func TestRunPropose_ApplyForcesStagedToLand(t *testing.T) {
	root := proposeInit(t)
	f := &proposeFlags{
		root: root, intent: "record_decision", op: "append_section",
		path: "decisions.md", heading: "Use X", headingLevel: 2,
		content: decisionContent,
		sources: []string{"user:meeting-2026-05-29"}, confidence: "confirmed",
		autoApply: true,
	}
	rep, err := runPropose(context.Background(), f, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != memory.StatusStaged {
		t.Fatalf("routing should still stage; status = %s", rep.Status)
	}
	if rep.Applied == nil || rep.Applied.Status != memory.StatusApplied {
		t.Fatalf("--apply did not land the staged proposal: %+v", rep.Applied)
	}
	if !strings.Contains(readMem(t, root, "decisions.md"), "use-x") {
		t.Error("decision not on disk after --apply")
	}
}

func TestRunPropose_FromJSONStdin(t *testing.T) {
	root := proposeInit(t)
	reqJSON := `{"intent":"add_pitfall","operations":[{"operation":"append_to_section","path":"pitfalls.md","section_id":"pitfalls","content":"- via json\n"}]}`
	f := &proposeFlags{root: root, fromJSON: "-"}
	rep, err := runPropose(context.Background(), f, strings.NewReader(reqJSON))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != memory.StatusApplied {
		t.Fatalf("status = %s (%s), want applied", rep.Status, rep.Reason)
	}
	if !strings.Contains(readMem(t, root, "pitfalls.md"), "via json") {
		t.Error("json-sourced bullet not written")
	}
}

func TestRunPropose_RejectsMissingProvenance(t *testing.T) {
	root := proposeInit(t)
	// record_decision requires sources; omit them → provenance_violation,
	// reported as a rejected result (not a Go error).
	f := &proposeFlags{
		root: root, intent: "record_decision", op: "append_section",
		path: "decisions.md", heading: "Use X", headingLevel: 2,
		content: decisionContent,
	}
	rep, err := runPropose(context.Background(), f, strings.NewReader(""))
	if err != nil {
		t.Fatalf("rejection should not be a Go error: %v", err)
	}
	if rep.Status != memory.StatusRejected {
		t.Fatalf("status = %s, want rejected", rep.Status)
	}
	if rep.Reason != memory.ReasonProvenanceViolation {
		t.Errorf("reason = %s, want %s", rep.Reason, memory.ReasonProvenanceViolation)
	}
}

func TestRunPropose_RequiresIntentAndOp(t *testing.T) {
	root := proposeInit(t)
	if _, err := runPropose(context.Background(), &proposeFlags{root: root, op: "create_file", path: "x"}, strings.NewReader("")); err == nil {
		t.Error("expected error when --intent missing")
	}
	if _, err := runPropose(context.Background(), &proposeFlags{root: root, intent: "update_shared"}, strings.NewReader("")); err == nil {
		t.Error("expected error when --op missing")
	}
}

func TestCobra_ProposeRejectedExitsNonZero(t *testing.T) {
	root := proposeInit(t)
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"propose", "--root", root,
		"--intent", "record_decision", "--op", "append_section",
		"--path", "decisions.md", "--heading", "Use X", "--heading-level", "2",
		"--content", decisionContent, // no --source → provenance violation
	})
	if err := cmd.Execute(); err == nil {
		t.Error("expected non-zero exit for rejected proposal")
	}
}
