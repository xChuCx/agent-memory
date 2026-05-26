package markdown

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureOp mirrors the JSON shape used by spike S1 and copied to
// internal/markdown/testdata/. Each fixture is a directory with in.md,
// op.json, out.md.
type fixtureOp struct {
	Op         string `json:"op"`
	SectionID  string `json:"section_id,omitempty"`
	Heading    string `json:"heading,omitempty"`
	Level      int    `json:"heading_level,omitempty"`
	Occurrence int    `json:"occurrence,omitempty"`
	Content    string `json:"content"`
}

// TestFixtures runs all golden-file cases under testdata/.
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

	var sec *Section
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
	if !ok || sec == nil {
		t.Fatalf("section not found in %s\n  request: id=%q heading=%q level=%d occ=%d\n  sections: %+v",
			dir, op.SectionID, op.Heading, op.Level, op.Occurrence, sections)
	}

	got, err := ReplaceSection(in, *sec, []byte(op.Content))
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}

	// Whole-output check.
	if !bytes.Equal(got, expected) {
		t.Errorf("output mismatch\n--- expected (%d bytes) ---\n%s--- got (%d bytes) ---\n%s---",
			len(expected), expected, len(got), got)
	}

	// Byte-preservation invariants: prefix and suffix outside the splice
	// range must be byte-identical between input and output.
	if !bytes.Equal(in[:sec.ByteStart], got[:sec.ByteStart]) {
		t.Errorf("prefix bytes mutated (positions before splice range)")
	}
	suffixIn := in[sec.ByteEnd:]
	suffixOutStart := len(got) - len(suffixIn)
	if suffixOutStart < 0 {
		t.Errorf("result length %d less than expected suffix length %d", len(got), len(suffixIn))
		return
	}
	if !bytes.Equal(suffixIn, got[suffixOutStart:]) {
		t.Errorf("suffix bytes mutated (positions after splice range)")
	}
}

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
		t.Errorf("section 0: %+v", sections[0])
	}
	if sections[1].HeadingText != "Sub" || sections[1].HeadingLevel != 2 {
		t.Errorf("section 1: %+v", sections[1])
	}
	if sections[2].HeadingText != "Other" || sections[2].HeadingLevel != 2 {
		t.Errorf("section 2: %+v", sections[2])
	}
	if sections[0].ByteEnd != len(src) {
		t.Errorf("Top.ByteEnd = %d, want %d (EOF)", sections[0].ByteEnd, len(src))
	}
	if sections[1].ByteEnd != sections[2].ByteStart {
		t.Errorf("Sub.ByteEnd=%d should equal Other.ByteStart=%d",
			sections[1].ByteEnd, sections[2].ByteStart)
	}
	if sections[2].ByteEnd != len(src) {
		t.Errorf("Other.ByteEnd = %d, want %d (EOF)", sections[2].ByteEnd, len(src))
	}
}

func TestParseSections_ContentHashStable(t *testing.T) {
	src := []byte("## A\n\nbody\n")
	a, _ := ParseSections(src)
	b, _ := ParseSections(src)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 section each, got %d/%d", len(a), len(b))
	}
	if a[0].ContentHash != b[0].ContentHash {
		t.Errorf("ContentHash should be deterministic: %q vs %q", a[0].ContentHash, b[0].ContentHash)
	}
	if !strings.HasPrefix(a[0].ContentHash, "sha256:") {
		t.Errorf("ContentHash should start with 'sha256:': %q", a[0].ContentHash)
	}
}

func TestParseSections_ContentHashChangesOnEdit(t *testing.T) {
	srcA := []byte("## A\n\nbody one\n")
	srcB := []byte("## A\n\nbody two\n")
	a, _ := ParseSections(srcA)
	b, _ := ParseSections(srcB)
	if a[0].ContentHash == b[0].ContentHash {
		t.Errorf("ContentHash should differ when body changes")
	}
}

func TestFindByID(t *testing.T) {
	src := []byte("## A\n<!-- @id: alpha -->\n\nbody\n\n## B\n<!-- @id: beta -->\n\nbody\n")
	secs, _ := ParseSections(src)
	if got, ok := FindByID(secs, "beta"); !ok {
		t.Error("FindByID(beta) returned !ok")
	} else if got.HeadingText != "B" {
		t.Errorf("got heading %q, want B", got.HeadingText)
	}
	if _, ok := FindByID(secs, "missing"); ok {
		t.Error("FindByID(missing) returned ok unexpectedly")
	}
}

func TestFindByHeading_Occurrence(t *testing.T) {
	src := []byte("## Same\n\nfirst\n\n## Other\n\n## Same\n\nsecond\n")
	secs, _ := ParseSections(src)
	first, ok := FindByHeading(secs, "Same", 2, 1)
	if !ok {
		t.Fatal("first occurrence not found")
	}
	second, ok := FindByHeading(secs, "Same", 2, 2)
	if !ok {
		t.Fatal("second occurrence not found")
	}
	if first.ByteStart == second.ByteStart {
		t.Error("two occurrences have the same ByteStart")
	}
	if _, ok := FindByHeading(secs, "Same", 2, 3); ok {
		t.Error("third occurrence should not exist")
	}
}

func TestFindAnchorID_StrictPositional(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"basic", "## Heading\n<!-- @id: my-id -->\n\nbody\n", "my-id"},
		{"no anchor", "## Heading\n\nbody\n", ""},
		{"different comment", "## Heading\n<!-- unrelated -->\n", ""},
		{"with whitespace", "## Heading\n<!-- @id:   spaced-id   -->\n", "spaced-id"},
		{"blank line slack", "## Heading\n\n<!-- @id: deferred -->\n", "deferred"},
		{"does not cross next heading", "# Outer\n\n## Inner\n<!-- @id: inner -->\n", ""},
		{"two blanks too far", "# Outer\n\n\n<!-- @id: too-far -->\n", ""},
		{"intervening text stops scan", "## H\n\nSome body.\n<!-- @id: too-late -->\n", ""},
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
