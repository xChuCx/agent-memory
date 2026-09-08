package vtp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJCS_AstralCharacters_UTF16CodeUnitSorting(t *testing.T) {
	// \uD83D\uDCA9 (pile of poo, U+1F4A9) vs \uE000 (Private Use BMP).
	// In UTF-16: U+1F4A9 -> 0xD83D 0xDCA9.
	// 0xD83D < 0xE000, so U+1F4A9 MUST precede U+E000 in RFC 8785 key ordering.
	m := map[string]int{
		"\uE000":     1,
		"\U0001F4A9": 2,
	}
	out, err := CanonicalJSON(m)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	s := string(out)
	pooIdx := strings.Index(s, "\U0001F4A9")
	bmpIdx := strings.Index(s, "\uE000")
	if pooIdx == -1 || bmpIdx == -1 || pooIdx > bmpIdx {
		t.Fatalf("RFC 8785 UTF-16 sort order violated: expected U+1F4A9 before U+E000, got %s", s)
	}
}

func TestJCS_ControlCharsAndLineSeparators(t *testing.T) {
	// U+2028 (LINE SEPARATOR) and U+2029 (PARAGRAPH SEPARATOR) must NOT be escaped per RFC 8785.
	// Control chars < 0x20 must be escaped as \u00xx.
	input := map[string]string{
		"ctrl": "\x00\x1f\n\t",
		"sep":  "\u2028\u2029",
	}
	out, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `\u0000`) || !strings.Contains(s, `\u001f`) {
		t.Fatalf("expected escaped control characters, got: %s", s)
	}
	if !strings.Contains(s, "\u2028") || !strings.Contains(s, "\u2029") {
		t.Fatalf("expected literal U+2028 and U+2029 characters, got: %s", s)
	}
}

func TestJCS_NegativeZero(t *testing.T) {
	input := map[string]json.Number{
		"a": json.Number("-0"),
		"b": json.Number("-0.0"),
	}
	out, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	s := string(out)
	expected := `{"a":0,"b":0}`
	if s != expected {
		t.Fatalf("expected %s, got %s", expected, s)
	}
}

func TestJCS_RejectsNaNAndInfinity(t *testing.T) {
	badNumbers := []string{"NaN", "Infinity", "-Infinity"}
	for _, bn := range badNumbers {
		input := map[string]json.Number{"num": json.Number(bn)}
		_, err := CanonicalJSON(input)
		if err == nil {
			t.Fatalf("expected error for non-finite number %q, got nil", bn)
		}
	}
}

func TestJCS_LoneSurrogatesRejected(t *testing.T) {
	// \xed\xa0\x80 is the CESU-8/surrogate representation of U+D800 (disallowed in UTF-8)
	badStr := "\xed\xa0\x80"
	input := map[string]string{"key": badStr}
	_, err := CanonicalJSON(input)
	if err == nil {
		t.Fatalf("expected error for lone surrogate string, got nil")
	}
}

func TestJCS_NumberFormatting(t *testing.T) {
	cases := []struct {
		input json.Number
		want  string
	}{
		{json.Number("1000000000000000000000"), "1e21"},
		{json.Number("100000000000000000000"), "100000000000000000000"},
		{json.Number("100"), "100"},
		{json.Number("0.0000001"), "1e-7"},
		{json.Number("0.000001"), "0.000001"},
	}
	for _, c := range cases {
		out, err := CanonicalJSON(map[string]json.Number{"n": c.input})
		if err != nil {
			t.Fatalf("failed for %s: %v", c.input, err)
		}
		expected := `{"n":` + c.want + `}`
		if string(out) != expected {
			t.Errorf("for %s: got %s, want %s", c.input, string(out), expected)
		}
	}
}
