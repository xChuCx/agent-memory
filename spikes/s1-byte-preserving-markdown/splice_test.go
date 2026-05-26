package s1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type fixtureOp struct {
	Op         string `json:"op"`
	SectionID  string `json:"section_id,omitempty"`
	Heading    string `json:"heading,omitempty"`
	Level      int    `json:"heading_level,omitempty"`
	Occurrence int    `json:"occurrence,omitempty"`
	Content    string `json:"content"`
}

// TestFixtures runs all golden-file fixtures under testdata/.
// Each fixture directory must contain in.md, op.json, out.md.
func TestFixtures(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			runFixture(t, filepath.Join("testdata", name))
		})
	}
}

func runFixture(t *testing.T, dir string) {
	t.Helper()

	in, err := os.ReadFile(filepath.Join(dir, "in.md"))
	if err != nil {
		t.Fatalf("read in.md: %v", err)
	}
	opBytes, err := os.ReadFile(filepath.Join(dir, "op.json"))
	if err != nil {
		t.Fatalf("read op.json: %v", err)
	}
	expected, err := os.ReadFile(filepath.Join(dir, "out.md"))
	if err != nil {
		t.Fatalf("read out.md: %v", err)
	}

	var op fixtureOp
	if err := json.Unmarshal(opBytes, &op); err != nil {
		t.Fatalf("parse op.json: %v", err)
	}

	sections, err := ParseSections(in)
	if err != nil {
		t.Fatalf("ParseSections: %v", err)
	}

	var sec Section
	var ok bool
	if op.SectionID != "" {
		sec, ok = FindByID(sections, op.SectionID)
	} else {
		occ := op.Occurrence
		if occ == 0 {
			occ = 1
		}
		sec, ok = FindByHeading(sections, op.Heading, op.Level, occ)
	}
	if !ok {
		t.Fatalf("section not found in %s\n  request: id=%q heading=%q level=%d occ=%d\n  available sections: %+v",
			dir, op.SectionID, op.Heading, op.Level, op.Occurrence, sections)
	}

	got, err := ReplaceSection(in, sec, []byte(op.Content))
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	// Whole-output check.
	if !bytes.Equal(got, expected) {
		t.Errorf("output mismatch\n--- expected (%d bytes) ---\n%s--- got (%d bytes) ---\n%s---",
			len(expected), expected, len(got), got)
	}

	// Byte-preservation invariants: prefix and suffix outside the splice range
	// must be byte-identical in input and output.
	if !bytes.Equal(in[:sec.ByteStart], got[:sec.ByteStart]) {
		t.Errorf("prefix bytes mutated (positions before splice range)\nin prefix:  %q\ngot prefix: %q",
			in[:sec.ByteStart], got[:sec.ByteStart])
	}

	suffixIn := in[sec.ByteEnd:]
	suffixOutStart := len(got) - len(suffixIn)
	if suffixOutStart < 0 {
		t.Errorf("result length %d less than expected suffix length %d", len(got), len(suffixIn))
		return
	}
	if !bytes.Equal(suffixIn, got[suffixOutStart:]) {
		t.Errorf("suffix bytes mutated (positions after splice range)\nin suffix:  %q\ngot suffix: %q",
			suffixIn, got[suffixOutStart:])
	}
}

// TestParseSectionsBasic verifies basic section parsing without going through fixtures.
func TestParseSectionsBasic(t *testing.T) {
	src := []byte("# Top\n\nIntro.\n\n## Sub\n\nBody.\n\n## Other\n\nMore.\n")
	sections, err := ParseSections(src)
	if err != nil {
		t.Fatalf("ParseSections: %v", err)
	}
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d: %+v", len(sections), sections)
	}
	if sections[0].HeadingText != "Top" || sections[0].HeadingLevel != 1 {
		t.Errorf("section 0: got %+v", sections[0])
	}
	if sections[1].HeadingText != "Sub" || sections[1].HeadingLevel != 2 {
		t.Errorf("section 1: got %+v", sections[1])
	}
	if sections[2].HeadingText != "Other" || sections[2].HeadingLevel != 2 {
		t.Errorf("section 2: got %+v", sections[2])
	}
	// Top is level-1; no other level-1 follows; Top section runs to EOF.
	if sections[0].ByteEnd != len(src) {
		t.Errorf("Top.ByteEnd = %d, want %d (EOF)", sections[0].ByteEnd, len(src))
	}
	// Sub ends at Other's start (next same-level heading).
	if sections[1].ByteEnd != sections[2].ByteStart {
		t.Errorf("Sub.ByteEnd=%d, Other.ByteStart=%d (should be equal)",
			sections[1].ByteEnd, sections[2].ByteStart)
	}
	// Other ends at EOF.
	if sections[2].ByteEnd != len(src) {
		t.Errorf("Other.ByteEnd = %d, want %d (EOF)", sections[2].ByteEnd, len(src))
	}
}

// TestSpliceRangeValidation checks that Splice rejects invalid ranges.
func TestSpliceRangeValidation(t *testing.T) {
	src := []byte("hello world")
	cases := []struct {
		name       string
		start, end int
	}{
		{"negative start", -1, 5},
		{"end beyond src", 0, len(src) + 1},
		{"start after end", 5, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Splice(src, c.start, c.end, []byte("X"))
			if err == nil {
				t.Errorf("expected error for [%d, %d)", c.start, c.end)
			}
		})
	}
}

// TestAnchorIDExtraction verifies the @id anchor parser.
func TestAnchorIDExtraction(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"basic", "## Heading\n<!-- @id: my-id -->\n\nBody.\n", "my-id"},
		{"no anchor", "## Heading\n\nBody.\n", ""},
		{"different comment", "## Heading\n<!-- unrelated -->\n", ""},
		{"with extra whitespace", "## Heading\n<!-- @id:   spaced-id   -->\n", "spaced-id"},
		{"blank line before anchor", "## Heading\n\n<!-- @id: deferred -->\n", "deferred"},
		// Regression: anchor finder must not leak across heading boundaries.
		// Previously, a 256-byte forward scan caused a level-1 heading without
		// its own anchor to falsely adopt the next section's anchor.
		{"does not cross next heading", "# Outer\n\n## Inner\n<!-- @id: inner-anchor -->\n", ""},
		{"does not cross with two blanks", "# Outer\n\n\n<!-- @id: too-far -->\n", ""},
		{"does not cross intervening text", "## Heading\n\nSome body text.\n<!-- @id: too-late -->\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findAnchorID([]byte(c.src), 0)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
