package memory

import (
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// mkOp builds a concrete Operation of the requested kind with placeholder
// fields. DecideRouting only consults Kind(), so the payload content is
// irrelevant here.
func mkOp(kind string) Operation {
	switch kind {
	case "create_file":
		return &CreateFile{FilePath: "x.md", Content: []byte("# x\n")}
	case "replace_section":
		return &ReplaceSection{FilePath: "x.md", Heading: "X", Level: 1, Content: []byte("# X\n")}
	case "append_section":
		return &AppendSection{FilePath: "x.md", Heading: "X", Level: 2, Content: []byte("## X\n")}
	case "append_to_section":
		return &AppendToSection{FilePath: "x.md", Heading: "X", Level: 1, Content: []byte("body\n")}
	case "replace_section_content":
		return &ReplaceSectionContent{FilePath: "x.md", Heading: "X", Level: 1, Content: []byte("body\n")}
	}
	return nil
}

func TestDecideRouting_AllIntents(t *testing.T) {
	man := config.DefaultManifest()
	op := mkOp("replace_section")

	cases := []struct {
		intent   Intent
		wantMode schema.ApprovalMode
	}{
		{IntentUpdateCurrent, schema.ApprovalApply},
		{IntentUpdateShared, schema.ApprovalApply},
		{IntentSessionLog, schema.ApprovalApply},
		{IntentRecordDecision, schema.ApprovalStage},
		{IntentRefreshModule, schema.ApprovalStage},
		{IntentUpdateConventions, schema.ApprovalStage},
		{IntentArchiveStale, schema.ApprovalStage},
	}
	for _, c := range cases {
		t.Run(string(c.intent), func(t *testing.T) {
			r, err := DecideRouting(c.intent, op, man)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if r.Mode != c.wantMode {
				t.Errorf("Mode = %q, want %q (reason: %s)", r.Mode, c.wantMode, r.Reason)
			}
		})
	}
}

func TestDecideRouting_AddPitfall_AppendVsReplace(t *testing.T) {
	man := config.DefaultManifest()

	r, err := DecideRouting(IntentAddPitfall, mkOp("append_to_section"), man)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if r.Mode != schema.ApprovalApply {
		t.Errorf("append_to_section: Mode = %q, want apply", r.Mode)
	}

	r, err = DecideRouting(IntentAddPitfall, mkOp("replace_section"), man)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if r.Mode != schema.ApprovalStage {
		t.Errorf("replace_section: Mode = %q, want stage", r.Mode)
	}
}

func TestDecideRouting_UnknownIntent(t *testing.T) {
	man := config.DefaultManifest()
	_, err := DecideRouting(Intent("not_a_real_intent"), mkOp("create_file"), man)
	if err == nil {
		t.Fatal("expected error for unknown intent")
	}
	if !strings.Contains(err.Error(), "unknown intent") {
		t.Errorf("err = %q, want mention of 'unknown intent'", err)
	}
}

func TestDecideRouting_NilManifest(t *testing.T) {
	_, err := DecideRouting(IntentUpdateCurrent, mkOp("create_file"), nil)
	if err == nil {
		t.Fatal("expected error for nil manifest")
	}
}

func TestDecideRouting_ManifestOverride(t *testing.T) {
	// User downgrades sessions to stage in their manifest. Routing should
	// reflect that.
	man := config.DefaultManifest()
	man.Updates.Approval.Sessions = schema.ApprovalStage

	r, err := DecideRouting(IntentSessionLog, mkOp("append_to_section"), man)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if r.Mode != schema.ApprovalStage {
		t.Errorf("Mode = %q, want stage after override", r.Mode)
	}
}

func TestCombineRoutings_AnyStageWins(t *testing.T) {
	c := CombineRoutings([]Routing{
		{Mode: schema.ApprovalApply, Reason: "a"},
		{Mode: schema.ApprovalStage, Reason: "b"},
		{Mode: schema.ApprovalApply, Reason: "c"},
	})
	if c.Mode != schema.ApprovalStage {
		t.Errorf("Mode = %q, want stage (one stage in the mix)", c.Mode)
	}
}

func TestCombineRoutings_AnyServerOnlyWins(t *testing.T) {
	c := CombineRoutings([]Routing{
		{Mode: schema.ApprovalApply, Reason: "a"},
		{Mode: schema.ApprovalStage, Reason: "b"},
		{Mode: schema.ApprovalServerOnly, Reason: "c"},
	})
	if c.Mode != schema.ApprovalServerOnly {
		t.Errorf("Mode = %q, want server_only", c.Mode)
	}
}

func TestCombineRoutings_AllApplyStaysApply(t *testing.T) {
	c := CombineRoutings([]Routing{
		{Mode: schema.ApprovalApply, Reason: "a"},
		{Mode: schema.ApprovalApply, Reason: "b"},
	})
	if c.Mode != schema.ApprovalApply {
		t.Errorf("Mode = %q, want apply", c.Mode)
	}
}

func TestCombineRoutings_Empty(t *testing.T) {
	c := CombineRoutings(nil)
	if c.Mode != schema.ApprovalApply {
		t.Errorf("Mode = %q, want apply for empty set", c.Mode)
	}
}

func TestIsValidIntent(t *testing.T) {
	for _, i := range AllIntents {
		if !IsValidIntent(i) {
			t.Errorf("AllIntents member %q reported invalid", i)
		}
	}
	if IsValidIntent("garbage") {
		t.Error("garbage reported valid")
	}
	if IsValidIntent("") {
		t.Error("empty intent reported valid")
	}
}
