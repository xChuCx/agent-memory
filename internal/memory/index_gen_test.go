package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// BuildIndexContent — structure + counts
// =============================================================================

func TestBuildIndexContent_StructureAndAlwaysInclude(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	content, err := BuildIndexContent(memDir, sch)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)
	for _, want := range []string{
		"# Agent Memory Index",
		indexGeneratedComment,
		"## Always include",
		"local/current.<branch>.md",
		"local/current.shared.md",
		"conventions.md",
		"## Topic map",
		"## Archive",
		"## Freshness",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("index missing %q\n---\n%s", want, s)
		}
	}
}

func TestBuildIndexContent_DecisionStatusCounts(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	// Replace decisions.md with two active + one superseded.
	dec := "# Decisions\n<!-- @id: decisions -->\n\n" +
		"## A\n<!-- @id: a -->\n\n**Status:** active\n\nbody\n\n" +
		"## B\n<!-- @id: b -->\n\n**Status:** active\n\nbody\n\n" +
		"## C\n<!-- @id: c -->\n\n**Status:** superseded\n\nbody\n"
	if err := os.WriteFile(filepath.Join(memDir, "decisions.md"), []byte(dec), 0644); err != nil {
		t.Fatal(err)
	}
	content, _ := BuildIndexContent(memDir, sch)
	s := string(content)
	if !strings.Contains(s, "decisions.md — durable architecture/product decisions (2 active, 1 superseded)") {
		t.Errorf("decision counts wrong:\n%s", s)
	}
}

func TestBuildIndexContent_DecisionUnspecifiedStatus(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	dec := "# Decisions\n<!-- @id: decisions -->\n\n" +
		"## A\n<!-- @id: a -->\n\nno status field here\n"
	if err := os.WriteFile(filepath.Join(memDir, "decisions.md"), []byte(dec), 0644); err != nil {
		t.Fatal(err)
	}
	content, _ := BuildIndexContent(memDir, sch)
	if !strings.Contains(string(content), "1 unspecified") {
		t.Errorf("expected 1 unspecified:\n%s", content)
	}
}

func TestBuildIndexContent_PitfallEntryCount(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	// updateFixture seeds pitfalls.md with one anchored section (stale-lock).
	content, _ := BuildIndexContent(memDir, sch)
	if !strings.Contains(string(content), "pitfalls.md — known traps (1 entry)") {
		t.Errorf("pitfall count wrong (want '1 entry'):\n%s", content)
	}
}

func TestBuildIndexContent_ListsModulesSorted(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	mods := filepath.Join(memDir, "modules")
	for _, n := range []string{"payments.md", "auth.md"} {
		if err := os.WriteFile(filepath.Join(mods, n),
			[]byte("# "+n+"\n<!-- @id: "+n+" -->\n\nbody\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	content, _ := BuildIndexContent(memDir, sch)
	s := string(content)
	ai := strings.Index(s, "modules/auth.md")
	pi := strings.Index(s, "modules/payments.md")
	if ai < 0 || pi < 0 {
		t.Fatalf("modules not listed:\n%s", s)
	}
	if ai > pi {
		t.Errorf("modules not sorted (auth should precede payments):\n%s", s)
	}
}

func TestBuildIndexContent_ArchiveCount(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	arch := filepath.Join(memDir, "archive")
	for _, n := range []string{"2026-01-a.md", "2026-02-b.md"} {
		if err := os.WriteFile(filepath.Join(arch, n), []byte("# x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	content, _ := BuildIndexContent(memDir, sch)
	if !strings.Contains(string(content), "2 archived context(s)") {
		t.Errorf("archive count wrong:\n%s", content)
	}
}

// =============================================================================
// Determinism + no-op behaviour
// =============================================================================

func TestBuildIndexContent_Deterministic(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	a, _ := BuildIndexContent(memDir, sch)
	b, _ := BuildIndexContent(memDir, sch)
	if string(a) != string(b) {
		t.Errorf("BuildIndexContent not deterministic:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}

func TestRegenerateIndex_NoChangeNoWrite(t *testing.T) {
	memDir, _, sch := updateFixture(t)
	// First regen creates it.
	changed1, err := RegenerateIndex(memDir, sch)
	if err != nil {
		t.Fatal(err)
	}
	if !changed1 {
		t.Error("first RegenerateIndex should report changed=true (file absent)")
	}
	// Capture mtime, regen again — content identical → no write → changed=false.
	path := filepath.Join(memDir, "index.md")
	info1, _ := os.Stat(path)
	changed2, err := RegenerateIndex(memDir, sch)
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("second RegenerateIndex should report changed=false (content identical)")
	}
	info2, _ := os.Stat(path)
	if !info2.ModTime().Equal(info1.ModTime()) {
		t.Error("index.md was rewritten despite identical content (mtime moved)")
	}
}

// =============================================================================
// Regeneration as an apply side-effect
// =============================================================================

func TestApply_RegeneratesIndex(t *testing.T) {
	memDir, mf, sch := updateFixture(t)
	deps := UpdateDeps{Manifest: mf, Schema: sch, MemoryDir: memDir}

	// Seed an index.md so we can see it CHANGE after the apply.
	if _, err := RegenerateIndex(memDir, sch); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(memDir, "index.md"))
	// The decisions.md seed is just "# Decisions\n" — no anchored
	// sections — so the topic map reports "no entries yet" before the
	// apply.
	if !strings.Contains(string(before), "no entries yet") {
		t.Errorf("seed index should report 'no entries yet' for decisions:\n%s", before)
	}

	// Apply a decision (stages → apply).
	resp, err := ProposeUpdate(context.Background(),
		ProposeRequest{
			Intent:    IntentRecordDecision,
			Rationale: "index regen test",
			Sources:   []Source{{Type: "user", Ref: "t"}},
			Operations: []OperationInput{
				{
					Op:           "append_section",
					Path:         "decisions.md",
					Heading:      "Idx Decision",
					HeadingLevel: 2,
					Content:      "## Idx Decision\n<!-- @id: idx-decision -->\n\n**Date:** 2026-05-27\n**Status:** active\n**Confidence:** confirmed\n\nbody\n",
				},
			},
		}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusStaged {
		t.Fatalf("expected staged, got %s", resp.Status)
	}
	if _, err := ApplyStaged(context.Background(), resp.StagingID, deps); err != nil {
		t.Fatal(err)
	}

	after, _ := os.ReadFile(filepath.Join(memDir, "index.md"))
	if !strings.Contains(string(after), "1 active") {
		t.Errorf("index.md not regenerated to reflect the new decision:\n%s", after)
	}
}
