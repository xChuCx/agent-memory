package memory

import (
	"bytes"
	"strings"
	"testing"

	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
)

// ---------- ParseOperation ----------

func TestParseOperation_Dispatch(t *testing.T) {
	cases := map[string]string{
		"create_file":             "*memory.CreateFile",
		"replace_section":         "*memory.ReplaceSection",
		"append_section":          "*memory.AppendSection",
		"append_to_section":       "*memory.AppendToSection",
		"replace_section_content": "*memory.ReplaceSectionContent",
	}
	for kind := range cases {
		t.Run(kind, func(t *testing.T) {
			op, err := ParseOperation(OperationInput{Op: kind, Path: "x.md", Content: "# X\n"})
			if err != nil {
				t.Fatalf("ParseOperation(%q): %v", kind, err)
			}
			if op.Kind() != kind {
				t.Errorf("Kind() = %q, want %q", op.Kind(), kind)
			}
		})
	}
}

func TestParseOperation_UnknownKind(t *testing.T) {
	_, err := ParseOperation(OperationInput{Op: "nonexistent"})
	if err == nil {
		t.Error("expected error for unknown op kind")
	}
}

// ---------- CreateFile ----------

func TestCreateFile_PlanRejectExistingFile(t *testing.T) {
	op := &CreateFile{FilePath: "new.md", Content: []byte("# New\n"), IfExists: "reject"}
	// Empty src means file doesn't exist on disk.
	plan, err := op.Plan(nil)
	if err != nil {
		t.Fatalf("Plan on empty src: %v", err)
	}
	if string(plan.Replacement) != "# New\n" {
		t.Errorf("Replacement = %q", plan.Replacement)
	}
	// Plan with non-empty src must fail under reject.
	if _, err := op.Plan([]byte("existing\n")); err == nil {
		t.Error("expected error when file exists and if_exists=reject")
	}
}

func TestCreateFile_PlanAppend(t *testing.T) {
	op := &CreateFile{FilePath: "f.md", Content: []byte("appended\n"), IfExists: "append"}
	src := []byte("# Existing\n")
	plan, _ := op.Plan(src)
	if plan.ByteStart != len(src) || plan.ByteEnd != len(src) {
		t.Errorf("append plan: start=%d end=%d, want both %d", plan.ByteStart, plan.ByteEnd, len(src))
	}
}

func TestCreateFile_PlanReplace(t *testing.T) {
	op := &CreateFile{FilePath: "f.md", Content: []byte("new\n"), IfExists: "replace"}
	src := []byte("old content\n")
	plan, _ := op.Plan(src)
	if plan.ByteStart != 0 || plan.ByteEnd != len(src) {
		t.Errorf("replace plan: start=%d end=%d, want 0..%d", plan.ByteStart, plan.ByteEnd, len(src))
	}
}

func TestCreateFile_Targets_RejectIsAbsent(t *testing.T) {
	op := &CreateFile{FilePath: "f.md", Content: []byte("x"), IfExists: "reject"}
	ts := op.Targets()
	if ts[0].Policy != RequireFileAbsent {
		t.Errorf("policy = %v, want RequireFileAbsent", ts[0].Policy)
	}
}

func TestCreateFile_Targets_AppendIsPresent(t *testing.T) {
	op := &CreateFile{FilePath: "f.md", Content: []byte("x"), IfExists: "append"}
	if op.Targets()[0].Policy != RequireFilePresent {
		t.Errorf("policy != RequireFilePresent")
	}
}

func TestCreateFile_Validate(t *testing.T) {
	cases := map[string]struct {
		op      *CreateFile
		wantErr bool
	}{
		"happy":            {&CreateFile{FilePath: "f.md", Content: []byte("# X\n")}, false},
		"missing path":     {&CreateFile{Content: []byte("# X\n")}, true},
		"missing content":  {&CreateFile{FilePath: "f.md"}, true},
		"bad if_exists":    {&CreateFile{FilePath: "f.md", Content: []byte("# X\n"), IfExists: "garbage"}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.op.Validate(nil)
			if c.wantErr && err == nil {
				t.Error("expected error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// ---------- ReplaceSection ----------

const sampleMarkdown = "# Module\n<!-- @id: module -->\n\nIntro.\n\n## Token Rotation\n<!-- @id: token-rotation -->\n\nOld content.\n\n## Other\n<!-- @id: other -->\n\nOther body.\n"

func TestReplaceSection_PlanByID(t *testing.T) {
	op := &ReplaceSection{
		FilePath:  "modules/auth.md",
		SectionID: "token-rotation",
		Content:   []byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nNew content.\n\n"),
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	// Apply the splice and verify the result.
	out, err := agentmd.Splice([]byte(sampleMarkdown), []agentmd.SpliceOp{plan})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "New content.") {
		t.Errorf("expected new content in result:\n%s", out)
	}
	if strings.Contains(string(out), "Old content.") {
		t.Errorf("old content survived:\n%s", out)
	}
	// Other section preserved.
	if !strings.Contains(string(out), "Other body.") {
		t.Errorf("Other section lost:\n%s", out)
	}
}

func TestReplaceSection_PlanByHeading(t *testing.T) {
	op := &ReplaceSection{
		FilePath: "modules/auth.md",
		Heading:  "Token Rotation",
		Level:    2,
		Content:  []byte("## Token Rotation\n\nReplaced.\n\n"),
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := agentmd.Splice([]byte(sampleMarkdown), []agentmd.SpliceOp{plan})
	if !strings.Contains(string(out), "Replaced.") {
		t.Errorf("heading-based lookup didn't replace: %s", out)
	}
}

func TestReplaceSection_MissingSectionRejectByDefault(t *testing.T) {
	op := &ReplaceSection{
		FilePath:  "modules/auth.md",
		SectionID: "nonexistent",
		Content:   []byte("## X\n"),
	}
	if _, err := op.Plan([]byte(sampleMarkdown)); err == nil {
		t.Error("expected error for missing section")
	}
}

func TestReplaceSection_MissingSectionAppendsWhenIfMissingAppend(t *testing.T) {
	op := &ReplaceSection{
		FilePath:  "modules/auth.md",
		SectionID: "new-thing",
		Content:   []byte("## New Thing\n<!-- @id: new-thing -->\n\nbody\n"),
		IfMissing: "append",
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	if plan.ByteStart != len(sampleMarkdown) {
		t.Errorf("expected append at EOF, got ByteStart=%d", plan.ByteStart)
	}
}

func TestReplaceSection_Targets(t *testing.T) {
	op := &ReplaceSection{FilePath: "x.md", SectionID: "foo", Content: []byte("## foo\n")}
	ts := op.Targets()
	if len(ts) != 1 || ts[0].Policy != RequireSectionContentMatch {
		t.Errorf("targets = %+v", ts)
	}
}

// ---------- AppendSection ----------

func TestAppendSection_PlanToEOF(t *testing.T) {
	op := &AppendSection{
		FilePath: "f.md",
		Heading:  "New",
		Level:    2,
		Content:  []byte("## New\n<!-- @id: new -->\n\nbody\n"),
	}
	src := []byte("# Top\n\nintro\n")
	plan, err := op.Plan(src)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := agentmd.Splice(src, []agentmd.SpliceOp{plan})
	if !strings.Contains(string(out), "## New") {
		t.Errorf("new section not appended:\n%s", out)
	}
	// Original content unchanged.
	if !strings.HasPrefix(string(out), "# Top\n\nintro\n") {
		t.Errorf("prefix mutated:\n%s", out)
	}
}

func TestAppendSection_PlanWithoutTrailingNewline(t *testing.T) {
	// Source ends mid-line: the inserted heading must NOT glue to it.
	op := &AppendSection{
		FilePath: "f.md",
		Heading:  "New",
		Level:    2,
		Content:  []byte("## New\n\nbody\n"),
	}
	src := []byte("# Top")
	plan, _ := op.Plan(src)
	out, _ := agentmd.Splice(src, []agentmd.SpliceOp{plan})
	if bytes.Contains(out, []byte("Top## New")) {
		t.Errorf("heading collided with non-newline-terminated source:\n%s", out)
	}
}

func TestAppendSection_PlanIntoParent_FirstChildSlot(t *testing.T) {
	// Parent "module" (level 1) has child "token-rotation" (level 2). A new
	// h2 child should land at the first-child slot — before token-rotation,
	// not at the parent's range end (which would be after ## Other).
	op := &AppendSection{
		FilePath:        "modules/auth.md",
		ParentSectionID: "module",
		Heading:         "Sub",
		Level:           2,
		Content:         []byte("## Sub\n<!-- @id: sub -->\n\nsub body\n"),
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := agentmd.Splice([]byte(sampleMarkdown), []agentmd.SpliceOp{plan})
	subIdx := strings.Index(string(out), "## Sub")
	tokenIdx := strings.Index(string(out), "## Token Rotation")
	if subIdx < 0 || tokenIdx < 0 {
		t.Fatalf("missing markers\n%s", out)
	}
	if subIdx > tokenIdx {
		t.Errorf("Sub inserted after Token Rotation; expected before (first-child slot)")
	}
	// "Intro." paragraph must survive ABOVE the new Sub.
	introIdx := strings.Index(string(out), "Intro.")
	if introIdx < 0 || introIdx > subIdx {
		t.Errorf("Intro paragraph lost or moved below Sub")
	}
}

func TestAppendSection_PlanIntoParent_NoChildrenFallbackToParentEnd(t *testing.T) {
	// Parent "token-rotation" (level 2) has no h3 children in sampleMarkdown.
	// A new h3 should land at parent.ByteEnd — just before "## Other".
	op := &AppendSection{
		FilePath:        "modules/auth.md",
		ParentSectionID: "token-rotation",
		Heading:         "Implementation Notes",
		Level:           3,
		Content:         []byte("### Implementation Notes\n<!-- @id: impl-notes -->\n\nnotes\n"),
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := agentmd.Splice([]byte(sampleMarkdown), []agentmd.SpliceOp{plan})
	implIdx := strings.Index(string(out), "### Implementation Notes")
	otherIdx := strings.Index(string(out), "## Other")
	oldIdx := strings.Index(string(out), "Old content.")
	if implIdx < 0 || otherIdx < 0 || oldIdx < 0 {
		t.Fatalf("missing markers\n%s", out)
	}
	// Order: Old content < Implementation Notes < Other.
	if !(oldIdx < implIdx && implIdx < otherIdx) {
		t.Errorf("expected order old<impl<other, got old=%d impl=%d other=%d",
			oldIdx, implIdx, otherIdx)
	}
}

func TestAppendSection_Validate(t *testing.T) {
	cases := map[string]struct {
		op      *AppendSection
		wantErr bool
	}{
		"happy":             {&AppendSection{FilePath: "f.md", Heading: "X", Level: 2, Content: []byte("## X\n")}, false},
		"missing path":      {&AppendSection{Heading: "X", Level: 2, Content: []byte("## X\n")}, true},
		"missing heading":   {&AppendSection{FilePath: "f.md", Level: 2, Content: []byte("## X\n")}, true},
		"bad level":         {&AppendSection{FilePath: "f.md", Heading: "X", Level: 0, Content: []byte("## X\n")}, true},
		"missing content":   {&AppendSection{FilePath: "f.md", Heading: "X", Level: 2}, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := c.op.Validate(nil)
			if c.wantErr && err == nil {
				t.Error("expected error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("unexpected: %v", err)
			}
		})
	}
}

// ---------- AppendToSection ----------

func TestAppendToSection_AppendsAtSectionEnd(t *testing.T) {
	op := &AppendToSection{
		FilePath:  "pitfalls.md",
		SectionID: "token-rotation",
		Content:   []byte("- new bullet entry\n"),
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := agentmd.Splice([]byte(sampleMarkdown), []agentmd.SpliceOp{plan})
	if !strings.Contains(string(out), "- new bullet entry") {
		t.Errorf("bullet not appended:\n%s", out)
	}
	// Bullet appears between Token Rotation body and ## Other.
	bulletIdx := strings.Index(string(out), "- new bullet entry")
	otherIdx := strings.Index(string(out), "## Other")
	if bulletIdx > otherIdx {
		t.Errorf("bullet landed after ## Other (should be before)")
	}
	// Heading untouched.
	if !strings.Contains(string(out), "## Token Rotation\n<!-- @id: token-rotation -->") {
		t.Errorf("heading mutated")
	}
}

func TestAppendToSection_Targets_ResolvablePolicy(t *testing.T) {
	op := &AppendToSection{FilePath: "x.md", SectionID: "y", Content: []byte("x")}
	if op.Targets()[0].Policy != RequireSectionResolvable {
		t.Errorf("policy != RequireSectionResolvable")
	}
}

// ---------- ReplaceSectionContent ----------

func TestReplaceSectionContent_KeepsHeadingAndAnchor(t *testing.T) {
	op := &ReplaceSectionContent{
		FilePath:  "modules/auth.md",
		SectionID: "token-rotation",
		Content:   []byte("\nUpdated body only.\n\n"),
	}
	plan, err := op.Plan([]byte(sampleMarkdown))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := agentmd.Splice([]byte(sampleMarkdown), []agentmd.SpliceOp{plan})

	// Heading + anchor must survive.
	if !strings.Contains(string(out), "## Token Rotation\n<!-- @id: token-rotation -->") {
		t.Errorf("heading or anchor mutated:\n%s", out)
	}
	if !strings.Contains(string(out), "Updated body only.") {
		t.Errorf("body not updated:\n%s", out)
	}
	if strings.Contains(string(out), "Old content.") {
		t.Errorf("old body survived:\n%s", out)
	}
}

func TestReplaceSectionContent_RejectsHeadingInContent(t *testing.T) {
	op := &ReplaceSectionContent{
		FilePath:  "f.md",
		SectionID: "x",
		Content:   []byte("## Another\n\nbody\n"),
	}
	if err := op.Validate(nil); err == nil {
		t.Error("expected error: content must not start with a heading")
	}
}

func TestReplaceSectionContent_FindBodyStartWithoutAnchor(t *testing.T) {
	// Section without an @id anchor — body starts right after the heading.
	src := []byte("## Heading\n\nbody\n")
	// section starts at byte 0
	bodyStart := findSectionBodyStart(src, 0)
	if bodyStart != len("## Heading\n") {
		t.Errorf("bodyStart = %d, want %d", bodyStart, len("## Heading\n"))
	}
}

func TestReplaceSectionContent_FindBodyStartWithAnchor(t *testing.T) {
	src := []byte("## Heading\n<!-- @id: h -->\n\nbody\n")
	bodyStart := findSectionBodyStart(src, 0)
	want := len("## Heading\n<!-- @id: h -->\n")
	if bodyStart != want {
		t.Errorf("bodyStart = %d, want %d", bodyStart, want)
	}
}

// ---------- end-to-end ----------

// TestEndToEnd_ReplaceThenAppend simulates two ops in sequence: first
// replace_section, then append_to_section. Exercises the basic pipeline
// pattern T3.7 will build on.
func TestEndToEnd_ReplaceThenAppend(t *testing.T) {
	src := []byte(sampleMarkdown)

	op1 := &ReplaceSection{
		FilePath:  "modules/auth.md",
		SectionID: "token-rotation",
		Content:   []byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nFresh body.\n\n"),
	}
	plan1, err := op1.Plan(src)
	if err != nil {
		t.Fatal(err)
	}
	src, err = agentmd.Splice(src, []agentmd.SpliceOp{plan1})
	if err != nil {
		t.Fatal(err)
	}

	op2 := &AppendToSection{
		FilePath:  "modules/auth.md",
		SectionID: "token-rotation",
		Content:   []byte("- bullet from op2\n"),
	}
	plan2, err := op2.Plan(src)
	if err != nil {
		t.Fatal(err)
	}
	src, err = agentmd.Splice(src, []agentmd.SpliceOp{plan2})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"Fresh body.", "- bullet from op2", "## Other", "Other body."}
	for _, s := range want {
		if !strings.Contains(string(src), s) {
			t.Errorf("missing %q in final output:\n%s", s, src)
		}
	}
}

// TestPlan_RejectsForServerManaged covers that an Operation's Validate
// reports a sensible error — this is at the operation level, not the
// orchestrator level which has its own server_managed guard.
func TestValidate_NilSchemaIsAcceptable(t *testing.T) {
	// All ops should tolerate a nil schema at this validation layer;
	// category-policy enforcement is the orchestrator's job (T3.7).
	op := &ReplaceSection{
		FilePath:  "modules/auth.md",
		SectionID: "x",
		Content:   []byte("## x\n"),
	}
	if err := op.Validate(nil); err != nil {
		t.Errorf("Validate(nil schema) errored: %v", err)
	}
}

