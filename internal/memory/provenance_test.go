package memory

import (
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/schema"
)

func TestIsValidConfidence(t *testing.T) {
	cases := map[string]bool{
		"":              true, // empty allowed
		"confirmed":     true,
		"inferred":      true,
		"user-provided": true,
		"stale":         true,
		"unknown":       true,
		"definitely":    false,
		"Confirmed":     false, // case-sensitive
	}
	for in, want := range cases {
		if got := IsValidConfidence(in); got != want {
			t.Errorf("IsValidConfidence(%q) = %v, want %v", in, got, want)
		}
	}
}

// decisionsPolicy mirrors the recommended schema for decisions:
// sources required, allow only file/test/user, forbid external/inference.
func decisionsPolicy() schema.Provenance {
	return schema.Provenance{
		Required:             true,
		AllowedSourceTypes:   []string{"file", "test", "user"},
		ForbiddenSourceTypes: []string{"external", "inference"},
	}
}

func TestValidateProvenance_HappyPath(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources: []Source{
			{Type: "file", Ref: "internal/auth/refresh.go"},
			{Type: "test", Ref: "internal/auth/refresh_test.go"},
		},
		Confidence: "confirmed",
	})
	if len(v) != 0 {
		t.Errorf("expected no violations, got %v", v)
	}
}

func TestValidateProvenance_RequiredButEmpty(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    nil,
		Confidence: "confirmed",
	})
	if len(v) == 0 {
		t.Fatal("expected violation for missing required sources")
	}
	if !containsSubstr(v, "sources are required") {
		t.Errorf("expected message about required sources: %v", v)
	}
}

func TestValidateProvenance_ForbiddenType(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "external", Ref: "https://blog.example.com"}},
		Confidence: "confirmed",
	})
	if !containsSubstr(v, "forbidden") {
		t.Errorf("expected forbidden-type violation, got %v", v)
	}
}

func TestValidateProvenance_NotInAllowedSet(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "session", Ref: "2026-05-26"}},
		Confidence: "confirmed",
	})
	// "session" is not forbidden but is also not in the allowed list.
	if !containsSubstr(v, "not in allowed") {
		t.Errorf("expected not-in-allowed violation, got %v", v)
	}
}

func TestValidateProvenance_InvalidConfidence(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "file", Ref: "x.go"}},
		Confidence: "definitely",
	})
	if !containsSubstr(v, "confidence") {
		t.Errorf("expected confidence violation, got %v", v)
	}
}

func TestValidateProvenance_EmptyConfidenceAllowed(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "file", Ref: "x.go"}},
		Confidence: "",
	})
	// Empty confidence is fine; treat as "unknown".
	for _, msg := range v {
		if strings.Contains(msg, "confidence") {
			t.Errorf("empty confidence flagged as violation: %v", msg)
		}
	}
}

func TestValidateProvenance_RequiredForNewSections(t *testing.T) {
	policy := schema.Provenance{RequiredForNewSections: true}

	// Empty sources + creating new section → violation.
	v := ValidateProvenance(policy, ProvenanceContext{IsNewSection: true})
	if !containsSubstr(v, "new sections") {
		t.Errorf("expected new-section violation, got %v", v)
	}

	// Empty sources + NOT a new section → OK.
	v = ValidateProvenance(policy, ProvenanceContext{IsNewSection: false})
	if len(v) != 0 {
		t.Errorf("non-new section shouldn't require sources, got %v", v)
	}
}

func TestValidateProvenance_SourceWithoutType(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "", Ref: "something"}},
		Confidence: "confirmed",
	})
	if !containsSubstr(v, "type is required") {
		t.Errorf("expected type-required violation, got %v", v)
	}
}

func TestValidateProvenance_NoSchemaPolicyMeansLooseChecks(t *testing.T) {
	// Empty Provenance policy (no Required, no Allowed/Forbidden lists)
	// → any source is fine.
	v := ValidateProvenance(schema.Provenance{}, ProvenanceContext{
		Sources:    []Source{{Type: "external", Ref: "x"}},
		Confidence: "inferred",
	})
	if len(v) != 0 {
		t.Errorf("unconfigured policy shouldn't violate, got %v", v)
	}
}

func TestValidateProvenance_AllowedWithoutForbiddenStillExcludes(t *testing.T) {
	// AllowedSourceTypes set but ForbiddenSourceTypes empty: anything not
	// in Allowed is rejected as "not in allowed".
	policy := schema.Provenance{
		AllowedSourceTypes: []string{"file"},
	}
	v := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "test", Ref: "x"}},
	})
	if !containsSubstr(v, "not in allowed") {
		t.Errorf("expected not-in-allowed violation, got %v", v)
	}
}

func TestValidateProvenance_BothListsApplied(t *testing.T) {
	// A type in BOTH allowed and forbidden — both messages fire.
	policy := schema.Provenance{
		AllowedSourceTypes:   []string{"file", "test"},
		ForbiddenSourceTypes: []string{"file"},
	}
	v := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "file", Ref: "x"}},
	})
	if !containsSubstr(v, "forbidden") {
		t.Errorf("expected forbidden message, got %v", v)
	}
}

// containsSubstr reports whether any element of haystack contains needle.
func containsSubstr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
