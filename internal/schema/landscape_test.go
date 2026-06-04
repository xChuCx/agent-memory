package schema

import "testing"

// The minimal federation landscape kinds (design §6.3) ship in the default
// schema so a landscape/platform store can author + validate them. A normal
// repo simply never creates these files.
func TestDefaultSchema_LandscapeKinds(t *testing.T) {
	s := DefaultSchema()

	for _, name := range []string{"component", "contract", "actor"} {
		c, ok := s.Categories[name]
		if !ok {
			t.Fatalf("missing landscape category %q", name)
		}
		if c.SectionSchema == nil {
			t.Errorf("category %q has no SectionSchema", name)
		}
		if !c.AgentWritable || c.Approval != ApprovalStage {
			t.Errorf("category %q: want agent-writable + staged, got writable=%v approval=%q",
				name, c.AgentWritable, c.Approval)
		}
	}

	// contract carries two enum-constrained required fields (Kind, Direction).
	c := s.Categories["contract"]
	if got := len(c.SectionSchema.PerSectionRequiredFields); got != 2 {
		t.Fatalf("contract required fields = %d, want 2", got)
	}
	byName := map[string][]string{}
	for _, f := range c.SectionSchema.PerSectionRequiredFields {
		byName[f.Name] = f.Enum
	}
	if len(byName["Kind"]) == 0 || len(byName["Direction"]) == 0 {
		t.Errorf("contract Kind/Direction should be enum-constrained: %+v", byName)
	}

	// CategoryForPath resolves the landscape files.
	if cat, ok := s.CategoryForPath("contracts.md"); !ok || cat.Name != "contract" {
		t.Errorf("CategoryForPath(contracts.md) = %q, %v", cat.Name, ok)
	}
}
