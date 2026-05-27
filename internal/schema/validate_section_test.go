package schema

import (
	"strings"
	"testing"
)

// helper: a decisions-like category with Date / Status / Confidence rules.
func decisionsLikeCategory() Category {
	return Category{
		Name: "decisions",
		File: "decisions.md",
		SectionSchema: &SectionSchema{
			PerSectionRequiredFields: []FieldSpec{
				{Name: "Date", Pattern: `^\d{4}-\d{2}-\d{2}$`},
				{Name: "Status", Enum: []string{"active", "superseded", "deprecated"}},
				{Name: "Confidence", Enum: []string{"confirmed", "inferred", "user-provided"}},
				{Name: "Context"},
				{Name: "Decision"},
				{Name: "Consequences"},
			},
			PerSectionOptionalFields: []FieldSpec{
				{Name: "Sources"},
				{Name: "Supersedes"},
			},
		},
	}
}

func TestValidateSection_NoSchemaReturnsNil(t *testing.T) {
	cat := Category{Name: "free", SectionSchema: nil}
	got := ValidateSection(cat, []byte("anything\ngoes here\n"))
	if got != nil {
		t.Errorf("expected nil violations, got %+v", got)
	}
}

func TestValidateSection_AllFieldsValid(t *testing.T) {
	cat := decisionsLikeCategory()
	body := []byte(`Date: 2026-05-26
Status: active
Confidence: confirmed

Context: We need a transactional store.

Decision: Use Postgres.

Consequences: Operational burden increases.

Sources: internal/storage/postgres.go
`)
	v := ValidateSection(cat, body)
	if len(v) != 0 {
		t.Errorf("expected no violations, got %+v", v)
	}
}

func TestValidateSection_MissingRequiredField(t *testing.T) {
	cat := decisionsLikeCategory()
	body := []byte(`Status: active
Confidence: confirmed
Context: x
Decision: y
Consequences: z
`)
	v := ValidateSection(cat, body)
	if len(v) == 0 {
		t.Fatal("expected at least one violation")
	}
	found := false
	for _, x := range v {
		if x.Field == "Date" && strings.Contains(x.Message, "missing") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing-Date violation, got %+v", v)
	}
}

func TestValidateSection_PatternMismatch(t *testing.T) {
	cat := decisionsLikeCategory()
	body := []byte(`Date: not-a-date
Status: active
Confidence: confirmed
Context: c
Decision: d
Consequences: x
`)
	v := ValidateSection(cat, body)
	found := false
	for _, x := range v {
		if x.Field == "Date" && strings.Contains(x.Message, "pattern") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pattern violation for Date, got %+v", v)
	}
}

func TestValidateSection_EnumViolation(t *testing.T) {
	cat := decisionsLikeCategory()
	body := []byte(`Date: 2026-05-26
Status: maybe
Confidence: confirmed
Context: c
Decision: d
Consequences: x
`)
	v := ValidateSection(cat, body)
	found := false
	for _, x := range v {
		if x.Field == "Status" && strings.Contains(x.Message, "allowed values") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected enum violation for Status, got %+v", v)
	}
}

func TestValidateSection_OptionalFieldEnumIfPresent(t *testing.T) {
	cat := Category{
		Name: "test",
		SectionSchema: &SectionSchema{
			PerSectionOptionalFields: []FieldSpec{
				{Name: "Mode", Enum: []string{"auto", "manual"}},
			},
		},
	}
	// Present but with wrong value.
	v := ValidateSection(cat, []byte("Mode: weird\n"))
	if len(v) == 0 {
		t.Error("expected violation for invalid optional value")
	}
	// Absent — no violation (it's optional).
	v = ValidateSection(cat, []byte("Other: thing\n"))
	if len(v) != 0 {
		t.Errorf("expected no violation when optional field absent, got %+v", v)
	}
}

func TestParseFieldLines_SkipsURLs(t *testing.T) {
	// URLs contain colons but are not field declarations.
	body := []byte(`Date: 2026-05-26
See: https://example.com for details
`)
	fields := parseFieldLines(body)
	if _, ok := fields["https"]; ok {
		t.Errorf("URL was misread as a field: %+v", fields)
	}
	if fields["Date"] != "2026-05-26" {
		t.Errorf("Date not parsed: %+v", fields)
	}
}

func TestParseFieldLines_SkipsIndentedAndBullets(t *testing.T) {
	body := []byte(`Date: 2026-05-26
- a bullet: with a colon
  Indented: continuation
Status: active
`)
	fields := parseFieldLines(body)
	if _, ok := fields["a bullet"]; ok {
		t.Errorf("bullet line was misread: %+v", fields)
	}
	if _, ok := fields["Indented"]; ok {
		t.Errorf("indented line was misread: %+v", fields)
	}
	if fields["Status"] != "active" {
		t.Errorf("Status not parsed: %+v", fields)
	}
}

func TestParseFieldLines_DuplicatesFirstWins(t *testing.T) {
	body := []byte(`Date: 2026-05-26
Date: 1900-01-01
`)
	fields := parseFieldLines(body)
	if fields["Date"] != "2026-05-26" {
		t.Errorf("expected first occurrence to win, got %q", fields["Date"])
	}
}

func TestLooksLikeFieldName(t *testing.T) {
	cases := map[string]bool{
		"Date":           true,
		"Field Name":     true,
		"with-dash":      true,
		"with_underscore": true,
		"":               false,
		"https":          true, // accepted; the URL guard is elsewhere (indented "//" doesn't match field syntax)
		"with.dot":       false,
		"with/slash":     false,
		"123abc":         true,
	}
	for in, want := range cases {
		got := looksLikeFieldName(in)
		if got != want {
			t.Errorf("looksLikeFieldName(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestValidateSection_BadRegexInSchema(t *testing.T) {
	// If the schema author wrote an invalid regex, surface a useful error
	// rather than panicking.
	cat := Category{
		Name: "broken",
		SectionSchema: &SectionSchema{
			PerSectionRequiredFields: []FieldSpec{
				{Name: "Bad", Pattern: "[unclosed"},
			},
		},
	}
	v := ValidateSection(cat, []byte("Bad: anything\n"))
	if len(v) == 0 {
		t.Fatal("expected violation for invalid regex pattern")
	}
	if !strings.Contains(v[0].Message, "invalid regex") {
		t.Errorf("expected 'invalid regex' message, got %q", v[0].Message)
	}
}

func TestSectionViolationString(t *testing.T) {
	v := SectionViolation{Field: "Date", Message: "missing"}
	s := v.String()
	if !strings.Contains(s, "Date") || !strings.Contains(s, "missing") {
		t.Errorf("String() = %q", s)
	}

	v2 := SectionViolation{Message: "category-level issue"}
	if v2.String() != "category-level issue" {
		t.Errorf("String() without Field = %q", v2.String())
	}
}
