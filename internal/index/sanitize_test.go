package index

import "testing"

func TestSanitizeFTSMatch(t *testing.T) {
	cases := map[string]string{
		"":                     "",
		"   ":                  "",
		"---":                  "",
		"refresh token":        `"refresh" OR "token"`,
		"auto-apply":           `"auto" OR "apply"`,
		"memory.fetch_context": `"memory" OR "fetch" OR "context"`,
		"AND OR NEAR":          `"AND" OR "OR" OR "NEAR"`, // reserved words → literal terms
		`a "b" c`:              `"a" OR "b" OR "c"`,       // embedded quotes/operators stripped
		"x:y (z)":              `"x" OR "y" OR "z"`,       // column-filter + parens neutralized
		"café 2.0":             `"café" OR "2" OR "0"`,    // unicode letters survive
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

// TestSearch_MatchesAnyTerm_OR is the recall regression: a multi-word query
// where only ONE word is present must still match (implicit-AND used to
// return nothing here). BM25 then ranks across whatever matched.
func TestSearch_MatchesAnyTerm_OR(t *testing.T) {
	idx, ctx := openTestIndex(t)
	if err := idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("modules/auth.md", "rt", "Refresh", "refresh tokens rotate on every successful use"),
	}); err != nil {
		t.Fatal(err)
	}
	// "refresh" is present; "zzznotpresent" matches nothing. OR → 1 hit.
	res, err := idx.Search(ctx, "refresh zzznotpresent", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("OR query should match on 'refresh' alone, got %d hits", len(res))
	}
	if res[0].SectionID != "rt" {
		t.Errorf("SectionID = %q, want rt", res[0].SectionID)
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
