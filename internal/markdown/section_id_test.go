package markdown

import (
	"bytes"
	"strings"
	"testing"
)

// ---------- slugify ----------

func TestSlugify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Foo", "foo"},
		{"space", "Foo Bar", "foo-bar"},
		{"upper", "FOO BAR", "foo-bar"},
		{"punctuation", "foo, bar!", "foo-bar"},
		{"leading and trailing whitespace", "  foo  bar  ", "foo-bar"},
		{"consecutive separators", "foo---bar", "foo-bar"},
		{"all non-alnum", "!!!", ""},
		{"empty", "", ""},
		{"section number", "Section 42", "section-42"},
		{"trailing punctuation", "trailing!!!", "trailing"},
		{"design doc example", "Token Rotation", "token-rotation"},
		{"single char", "a", "a"},
		{"digits", "123", "123"},
		{"cyrillic stripped", "Привет", ""},
		{"mixed ascii cyrillic", "Foo Привет Bar", "foo-bar"},
		{"version-like", "v1.2.3", "v1-2-3"},
		{"underscore is non-alnum", "foo_bar", "foo-bar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := slugify(c.in)
			if got != c.want {
				t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSlugify_Truncation(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := slugify(long)
	if len(got) != 64 {
		t.Errorf("expected length 64, got %d (%q)", len(got), got)
	}
}

func TestSlugify_TruncationDoesNotLeaveTrailingDash(t *testing.T) {
	// 65th char would be a dash; truncation should re-trim.
	in := strings.Repeat("a", 64) + " end"
	got := slugify(in)
	if strings.HasSuffix(got, "-") {
		t.Errorf("got trailing dash: %q", got)
	}
	if len(got) > 64 {
		t.Errorf("length %d > 64: %q", len(got), got)
	}
}

// ---------- uniqueSlug ----------

func TestUniqueSlug_NoCollision(t *testing.T) {
	used := map[string]bool{}
	if got := uniqueSlug("foo", used); got != "foo" {
		t.Errorf("first call: %q, want foo", got)
	}
	if !used["foo"] {
		t.Errorf("uniqueSlug did not record %q in used", "foo")
	}
}

func TestUniqueSlug_CollisionChain(t *testing.T) {
	used := map[string]bool{}
	for i, want := range []string{"foo", "foo-2", "foo-3", "foo-4"} {
		got := uniqueSlug("foo", used)
		if got != want {
			t.Errorf("call %d: got %q, want %q", i, got, want)
		}
	}
}

func TestUniqueSlug_DifferentBases(t *testing.T) {
	used := map[string]bool{"foo": true, "bar": true}
	if got := uniqueSlug("baz", used); got != "baz" {
		t.Errorf("baz: got %q, want baz", got)
	}
	if got := uniqueSlug("foo", used); got != "foo-2" {
		t.Errorf("foo: got %q, want foo-2", got)
	}
}

// ---------- AssignMissingIDs ----------

func TestAssignMissingIDs_BasicAssignment(t *testing.T) {
	src := []byte("# Top\n\n## Token Rotation\n\nbody\n\n## Other\n\nbody\n")
	newSrc, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatalf("AssignMissingIDs: %v", err)
	}
	want := []string{"top", "token-rotation", "other"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], w)
		}
	}
	// All expected anchors must appear in the new source.
	for _, id := range want {
		anchor := "<!-- @id: " + id + " -->"
		if !bytes.Contains(newSrc, []byte(anchor)) {
			t.Errorf("anchor %q missing from output:\n%s", anchor, newSrc)
		}
	}
}

func TestAssignMissingIDs_Idempotent(t *testing.T) {
	src := []byte("## Foo\n\nbody\n\n## Bar\n\nbody\n")
	pass1, ids1, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	pass2, ids2, err := AssignMissingIDs(pass1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pass1, pass2) {
		t.Errorf("second pass mutated bytes\npass1:\n%s\npass2:\n%s", pass1, pass2)
	}
	if len(ids1) != len(ids2) {
		t.Fatalf("id-slice length differs: %d vs %d", len(ids1), len(ids2))
	}
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Errorf("ids[%d]: pass1=%q pass2=%q", i, ids1[i], ids2[i])
		}
	}
}

func TestAssignMissingIDs_PreservesExistingAnchors(t *testing.T) {
	src := []byte("## Foo\n<!-- @id: custom-name -->\n\nbody\n\n## Bar\n\nbody\n")
	newSrc, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != "custom-name" {
		t.Errorf("ids[0] = %q, want custom-name", ids[0])
	}
	if ids[1] != "bar" {
		t.Errorf("ids[1] = %q, want bar", ids[1])
	}
	if !bytes.Contains(newSrc, []byte("<!-- @id: custom-name -->")) {
		t.Error("existing custom-name anchor was lost")
	}
	if !bytes.Contains(newSrc, []byte("<!-- @id: bar -->")) {
		t.Error("new bar anchor was not inserted")
	}
}

func TestAssignMissingIDs_Collision(t *testing.T) {
	src := []byte("## Auth\n\nfirst\n\n## Auth\n\nsecond\n\n## Auth\n\nthird\n")
	_, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"auth", "auth-2", "auth-3"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("ids[%d] = %q, want %q", i, ids[i], w)
		}
	}
}

func TestAssignMissingIDs_CollisionWithExistingAnchor(t *testing.T) {
	// First "Auth" reserves the natural slug "auth" via its custom anchor.
	// Second "Auth" must skip "auth" and go straight to "auth-2".
	src := []byte("## Auth\n<!-- @id: auth -->\n\n## Auth\n\nbody\n")
	_, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != "auth" {
		t.Errorf("ids[0] = %q, want auth", ids[0])
	}
	if ids[1] != "auth-2" {
		t.Errorf("ids[1] = %q, want auth-2", ids[1])
	}
}

func TestAssignMissingIDs_EmptyHeadingFallback(t *testing.T) {
	src := []byte("## !!!\n\nbody\n")
	_, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != "section" {
		t.Errorf("ids[0] = %q, want section", ids[0])
	}
}

func TestAssignMissingIDs_EmptyHeadingsCollide(t *testing.T) {
	src := []byte("## !!!\n\none\n\n## ???\n\ntwo\n")
	_, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != "section" {
		t.Errorf("ids[0] = %q, want section", ids[0])
	}
	if ids[1] != "section-2" {
		t.Errorf("ids[1] = %q, want section-2", ids[1])
	}
}

func TestAssignMissingIDs_NoTrailingNewline(t *testing.T) {
	src := []byte("## Foo")
	newSrc, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	if ids[0] != "foo" {
		t.Errorf("ids[0] = %q, want foo", ids[0])
	}
	// Critical: anchor must NOT collide with the heading line.
	if bytes.Contains(newSrc, []byte("Foo<!--")) {
		t.Errorf("anchor collided with heading line:\n%s", newSrc)
	}
	want := []byte("## Foo\n<!-- @id: foo -->\n")
	if !bytes.Equal(newSrc, want) {
		t.Errorf("got:\n%q\nwant:\n%q", newSrc, want)
	}
}

func TestAssignMissingIDs_PreservesPreamble(t *testing.T) {
	src := []byte("preamble before any heading\n\n## Foo\n\nbody\n")
	newSrc, _, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	headingStart := bytes.Index(src, []byte("## Foo"))
	if headingStart < 0 {
		t.Fatal("test fixture broken: heading not found")
	}
	if !bytes.Equal(src[:headingStart], newSrc[:headingStart]) {
		t.Errorf("preamble bytes mutated\norig:\n%q\nnew:\n%q",
			src[:headingStart], newSrc[:headingStart])
	}
}

func TestAssignMissingIDs_AnchorPositioning(t *testing.T) {
	src := []byte("## Token Rotation\n\nbody\n")
	newSrc, _, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nbody\n")
	if !bytes.Equal(newSrc, want) {
		t.Errorf("layout wrong\ngot:\n%s\nwant:\n%s", newSrc, want)
	}
}

func TestAssignMissingIDs_LongHeadingTruncated(t *testing.T) {
	// 80-char heading text → slug must be at most 64 chars and not end with -.
	src := []byte("## " + strings.Repeat("Foo Bar ", 10) + "\n\nbody\n")
	_, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids[0]) > 64 {
		t.Errorf("slug too long: %d chars: %q", len(ids[0]), ids[0])
	}
	if strings.HasSuffix(ids[0], "-") {
		t.Errorf("slug ends with dash: %q", ids[0])
	}
}

func TestAssignMissingIDs_RoundTripWithParseSections(t *testing.T) {
	// After assignment, ParseSections must find every assigned ID as an
	// AnchorID. This is the round-trip property that makes
	// AssignMissingIDs idempotent.
	src := []byte("## Foo\n\n## Bar\n\n## Baz\n")
	newSrc, ids, err := AssignMissingIDs(src)
	if err != nil {
		t.Fatal(err)
	}
	secs, err := ParseSections(newSrc)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != len(ids) {
		t.Fatalf("section count mismatch: %d sections, %d ids", len(secs), len(ids))
	}
	for i, s := range secs {
		if s.AnchorID != ids[i] {
			t.Errorf("section %d (heading %q): AnchorID=%q, want %q",
				i, s.HeadingText, s.AnchorID, ids[i])
		}
	}
}
