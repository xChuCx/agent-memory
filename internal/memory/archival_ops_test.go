package memory

import (
	"bytes"
	"strings"
	"testing"

	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
)

// fileWithSection is a small fixture: a level-1 doc with one anchored
// level-2 section the archival ops can target.
var fileWithSection = []byte("# Module\n<!-- @id: module -->\n\n" +
	"## Legacy Flow\n<!-- @id: legacy-flow -->\n\n" +
	"Old cookie refresh logic.\n\n" +
	"## Keep Me\n<!-- @id: keep-me -->\n\n" +
	"Still relevant.\n")

// =============================================================================
// ArchiveSection
// =============================================================================

func TestArchiveSection_PlanReplacesSourceSection(t *testing.T) {
	op := &ArchiveSection{
		FilePath:    "modules/auth.md",
		SectionID:   "legacy-flow",
		ArchivePath: "archive/2026-05-legacy.md",
		Replacement: []byte("## Legacy Flow\n<!-- @id: legacy-flow -->\n\nArchived: see `archive/2026-05-legacy.md`.\n"),
	}
	splice, err := op.Plan(fileWithSection)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	out, err := agentmd.Splice(fileWithSection, []agentmd.SpliceOp{splice})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "Archived: see `archive/2026-05-legacy.md`") {
		t.Errorf("source section not replaced with stub:\n%s", s)
	}
	if strings.Contains(s, "Old cookie refresh logic.") {
		t.Errorf("original body should be gone from source:\n%s", s)
	}
	// The untouched sibling survives.
	if !strings.Contains(s, "Still relevant.") {
		t.Errorf("sibling section was disturbed:\n%s", s)
	}
}

func TestArchiveSection_ExtraFilesCopiesOriginal(t *testing.T) {
	op := &ArchiveSection{
		FilePath:    "modules/auth.md",
		SectionID:   "legacy-flow",
		ArchivePath: "archive/2026-05-legacy.md",
		Replacement: []byte("## Legacy Flow\n<!-- @id: legacy-flow -->\n\nstub.\n"),
	}
	extras, err := op.ExtraFiles(fileWithSection)
	if err != nil {
		t.Fatalf("ExtraFiles: %v", err)
	}
	if len(extras) != 1 {
		t.Fatalf("want 1 extra file, got %d", len(extras))
	}
	if extras[0].Path != "archive/2026-05-legacy.md" {
		t.Errorf("archive path = %q", extras[0].Path)
	}
	content := string(extras[0].Content)
	// Archive must carry the ORIGINAL content (heading + anchor + body),
	// not the stub.
	if !strings.Contains(content, "## Legacy Flow") {
		t.Errorf("archive missing original heading:\n%s", content)
	}
	if !strings.Contains(content, "Old cookie refresh logic.") {
		t.Errorf("archive missing original body:\n%s", content)
	}
	// Must NOT bleed into the sibling section.
	if strings.Contains(content, "Keep Me") {
		t.Errorf("archive captured the sibling section:\n%s", content)
	}
}

func TestArchiveSection_Targets(t *testing.T) {
	op := &ArchiveSection{
		FilePath:    "modules/auth.md",
		SectionID:   "legacy-flow",
		ArchivePath: "archive/x.md",
		Replacement: []byte("stub"),
	}
	targets := op.Targets()
	if len(targets) != 2 {
		t.Fatalf("want 2 targets, got %d", len(targets))
	}
	var sawSource, sawArchive bool
	for _, tg := range targets {
		if tg.Path == "modules/auth.md" && tg.Policy == RequireSectionContentMatch {
			sawSource = true
		}
		if tg.Path == "archive/x.md" && tg.Policy == RequireFileAbsent {
			sawArchive = true
		}
	}
	if !sawSource || !sawArchive {
		t.Errorf("targets wrong: %+v", targets)
	}
}

func TestArchiveSection_Validate(t *testing.T) {
	cases := []struct {
		name string
		op   *ArchiveSection
		ok   bool
	}{
		{"happy", &ArchiveSection{FilePath: "modules/a.md", SectionID: "x", ArchivePath: "archive/a.md", Replacement: []byte("# s\n")}, true},
		{"missing path", &ArchiveSection{SectionID: "x", ArchivePath: "archive/a.md", Replacement: []byte("s")}, false},
		{"missing section", &ArchiveSection{FilePath: "m.md", ArchivePath: "archive/a.md", Replacement: []byte("s")}, false},
		{"missing archive", &ArchiveSection{FilePath: "m.md", SectionID: "x", Replacement: []byte("s")}, false},
		{"archive not in archive/", &ArchiveSection{FilePath: "m.md", SectionID: "x", ArchivePath: "modules/a.md", Replacement: []byte("s")}, false},
		{"missing replacement", &ArchiveSection{FilePath: "m.md", SectionID: "x", ArchivePath: "archive/a.md"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.op.Validate(nil)
			if c.ok && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// =============================================================================
// RemoveSection
// =============================================================================

func TestRemoveSection_PlanSplicesOut(t *testing.T) {
	op := &RemoveSection{
		FilePath:    "modules/auth.md",
		SectionID:   "legacy-flow",
		ArchivePath: "archive/gone.md",
		Reason:      "superseded",
	}
	splice, err := op.Plan(fileWithSection)
	if err != nil {
		t.Fatal(err)
	}
	out, err := agentmd.Splice(fileWithSection, []agentmd.SpliceOp{splice})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "Legacy Flow") || strings.Contains(s, "Old cookie refresh logic.") {
		t.Errorf("removed section still present:\n%s", s)
	}
	if !strings.Contains(s, "Keep Me") {
		t.Errorf("sibling removed too:\n%s", s)
	}
	// Module heading survives.
	if !strings.Contains(s, "# Module") {
		t.Errorf("doc heading lost:\n%s", s)
	}
}

func TestRemoveSection_ExtraFilesArchivesWithReason(t *testing.T) {
	op := &RemoveSection{
		FilePath:    "modules/auth.md",
		SectionID:   "legacy-flow",
		ArchivePath: "archive/gone.md",
		Reason:      "Replaced by token-rotation",
	}
	extras, err := op.ExtraFiles(fileWithSection)
	if err != nil {
		t.Fatal(err)
	}
	if len(extras) != 1 {
		t.Fatalf("want 1 extra, got %d", len(extras))
	}
	content := string(extras[0].Content)
	if !strings.Contains(content, "<!-- removed from modules/auth.md: Replaced by token-rotation -->") {
		t.Errorf("archive missing reason comment:\n%s", content)
	}
	if !strings.Contains(content, "Old cookie refresh logic.") {
		t.Errorf("archive missing original body:\n%s", content)
	}
}

func TestRemoveSection_ExtraFilesNoReason(t *testing.T) {
	op := &RemoveSection{
		FilePath:    "modules/auth.md",
		SectionID:   "legacy-flow",
		ArchivePath: "archive/gone.md",
	}
	extras, err := op.ExtraFiles(fileWithSection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(extras[0].Content), "<!-- removed") {
		t.Errorf("no-reason archive shouldn't carry a removal comment:\n%s", extras[0].Content)
	}
}

// =============================================================================
// RenameHeading
// =============================================================================

func TestRenameHeading_PreservesAnchorAndBody(t *testing.T) {
	op := &RenameHeading{
		FilePath:   "modules/auth.md",
		SectionID:  "legacy-flow",
		NewHeading: "Refreshed Legacy Flow",
	}
	splice, err := op.Plan(fileWithSection)
	if err != nil {
		t.Fatal(err)
	}
	out, err := agentmd.Splice(fileWithSection, []agentmd.SpliceOp{splice})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "## Refreshed Legacy Flow") {
		t.Errorf("heading not renamed:\n%s", s)
	}
	if strings.Contains(s, "## Legacy Flow\n") {
		t.Errorf("old heading still present:\n%s", s)
	}
	// Anchor preserved (id unchanged).
	if !strings.Contains(s, "<!-- @id: legacy-flow -->") {
		t.Errorf("anchor lost on rename:\n%s", s)
	}
	// Body preserved.
	if !strings.Contains(s, "Old cookie refresh logic.") {
		t.Errorf("body disturbed on rename:\n%s", s)
	}
}

func TestRenameHeading_LevelChangeWithinOne(t *testing.T) {
	// legacy-flow is level 2; promoting to level 1 is delta -1, allowed.
	op := &RenameHeading{
		FilePath:        "modules/auth.md",
		SectionID:       "legacy-flow",
		NewHeading:      "Promoted",
		NewHeadingLevel: 1,
	}
	splice, err := op.Plan(fileWithSection)
	if err != nil {
		t.Fatalf("level -1 should be allowed: %v", err)
	}
	out, _ := agentmd.Splice(fileWithSection, []agentmd.SpliceOp{splice})
	if !bytes.Contains(out, []byte("\n# Promoted\n")) {
		t.Errorf("level-1 heading not produced:\n%s", out)
	}
}

func TestRenameHeading_LevelChangeBeyondOneRejected(t *testing.T) {
	// legacy-flow is level 2; jumping to level 4 is delta +2, rejected.
	op := &RenameHeading{
		FilePath:        "modules/auth.md",
		SectionID:       "legacy-flow",
		NewHeading:      "TooDeep",
		NewHeadingLevel: 4,
	}
	if _, err := op.Plan(fileWithSection); err == nil {
		t.Error("expected level ±1 constraint violation, got nil")
	}
}

func TestRenameHeading_Validate(t *testing.T) {
	cases := []struct {
		name string
		op   *RenameHeading
		ok   bool
	}{
		{"happy", &RenameHeading{FilePath: "m.md", SectionID: "x", NewHeading: "New"}, true},
		{"missing new_heading", &RenameHeading{FilePath: "m.md", SectionID: "x"}, false},
		{"bad level", &RenameHeading{FilePath: "m.md", SectionID: "x", NewHeading: "N", NewHeadingLevel: 9}, false},
		{"level 0 ok (keep)", &RenameHeading{FilePath: "m.md", SectionID: "x", NewHeading: "N", NewHeadingLevel: 0}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.op.Validate(nil)
			if c.ok && err != nil {
				t.Errorf("expected ok, got %v", err)
			}
			if !c.ok && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

// =============================================================================
// ParseOperation dispatch for the three new kinds
// =============================================================================

func TestParseOperation_M4Kinds(t *testing.T) {
	cases := []struct {
		in   OperationInput
		want string
	}{
		{OperationInput{Op: "archive_section", Path: "m.md", SectionID: "x", ArchivePath: "archive/a.md", Replacement: "s"}, "archive_section"},
		{OperationInput{Op: "remove_section", Path: "m.md", SectionID: "x", ArchivePath: "archive/a.md"}, "remove_section"},
		{OperationInput{Op: "rename_heading", Path: "m.md", SectionID: "x", NewHeading: "N"}, "rename_heading"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			op, err := ParseOperation(c.in)
			if err != nil {
				t.Fatalf("ParseOperation: %v", err)
			}
			if op.Kind() != c.want {
				t.Errorf("Kind() = %q, want %q", op.Kind(), c.want)
			}
		})
	}
}
