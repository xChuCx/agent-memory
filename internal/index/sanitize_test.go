package index

import "testing"

func TestSanitizeFTSMatch(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"   ":                  "",
		"---":                  "",
		"refresh token":        `"refresh" "token"`,
		"auto-apply":           `"auto" "apply"`,
		"memory.fetch_context": `"memory" "fetch" "context"`,
		"AND OR NEAR":          `"AND" "OR" "NEAR"`, // reserved words → literal terms
		`a "b" c`:              `"a" "b" "c"`,       // embedded quotes/operators stripped
		"x:y (z)":              `"x" "y" "z"`,       // column-filter + parens neutralized
		"café 2.0":             `"café" "2" "0"`,    // unicode letters survive
	}
	for in, want := range cases {
		if got := sanitizeFTSMatch(in); got != want {
			t.Errorf("sanitizeFTSMatch(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSearch_SpecialCharsDoNotCrash is the regression for the dogfooding
// find: a natural-language query containing a hyphen / FTS5 operator used to
// reach FTS5 MATCH verbatim and fail with "SQL logic error: no such column".
func TestSearch_SpecialCharsDoNotCrash(t *testing.T) {
	idx, ctx := openTestIndex(t)
	if err := idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("conventions.md", "auto", "Auto Apply", "we support an auto apply mode for the brave"),
	}); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{"auto-apply", "AND OR", "memory.fetch_context", `"unterminated`, "a:b"} {
		if _, err := idx.Search(ctx, q, 5); err != nil {
			t.Errorf("Search(%q) errored: %v", q, err)
		}
	}

	// And the hyphenated query still finds the section (auto AND apply present).
	res, err := idx.Search(ctx, "auto-apply", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Error("auto-apply should match the 'auto ... apply' section")
	}
}

// TestSearch_AllPunctuationIsEmpty confirms a query with no alphanumeric
// content is treated like an empty query (no rows, no error) rather than
// producing an invalid empty MATCH.
func TestSearch_AllPunctuationIsEmpty(t *testing.T) {
	idx, ctx := openTestIndex(t)
	if err := idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("conventions.md", "c", "Conventions", "body text"),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := idx.Search(ctx, "--- :: !!", 5)
	if err != nil {
		t.Fatalf("punctuation-only query errored: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("punctuation-only query should yield no hits, got %d", len(res))
	}
}
