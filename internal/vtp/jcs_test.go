package vtp

import (
	"encoding/json"
	"math"
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
		{json.Number("1000000000000000000000"), "1e+21"},
		{json.Number("100000000000000000000"), "100000000000000000000"},
		{json.Number("100"), "100"},
		{json.Number("0.0000001"), "1e-7"},
		{json.Number("0.000001"), "0.000001"},
		{json.Number("1e+30"), "1e+30"},
		{json.Number("1E30"), "1e+30"},
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

func TestJCS_RFC8785_AppendixB_Table1(t *testing.T) {
	// Table 1: ECMAScript-Compatible JSON Number Serialization Samples from RFC 8785 Appendix B
	table1 := []struct {
		bits     uint64
		expected string
		comment  string
	}{
		{0x0000000000000000, "0", "Zero"},
		{0x8000000000000000, "0", "Minus zero"},
		{0x0000000000000001, "5e-324", "Min pos number"},
		{0x8000000000000001, "-5e-324", "Min neg number"},
		{0x7fefffffffffffff, "1.7976931348623157e+308", "Max pos number"},
		{0xffefffffffffffff, "-1.7976931348623157e+308", "Max neg number"},
		{0x4340000000000000, "9007199254740992", "Max pos int (1)"},
		{0xc340000000000000, "-9007199254740992", "Max neg int (1)"},
		{0x4430000000000000, "295147905179352830000", "~2**68 (2)"},
		{0x44b52d02c7e14af5, "9.999999999999997e+22", ""},
		{0x44b52d02c7e14af6, "1e+23", ""},
		{0x44b52d02c7e14af7, "1.0000000000000001e+23", ""},
		{0x444b1ae4d6e2ef4e, "999999999999999700000", ""},
		{0x444b1ae4d6e2ef4f, "999999999999999900000", ""},
		{0x444b1ae4d6e2ef50, "1e+21", ""},
		{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7", ""},
		{0x3eb0c6f7a0b5ed8d, "0.000001", ""},
		{0x41b3de4355555553, "333333333.3333332", ""},
		{0x41b3de4355555554, "333333333.33333325", ""},
		{0x41b3de4355555555, "333333333.3333333", ""},
		{0x41b3de4355555556, "333333333.3333334", ""},
		{0x41b3de4355555557, "333333333.33333343", ""},
		{0xbecbf647612f3696, "-0.0000033333333333333333", ""},
		{0x43143ff3c1cb0959, "1424953923781206.2", "Round to even (4)"},
	}

	for _, entry := range table1 {
		val := math.Float64frombits(entry.bits)
		got, err := FormatJCSFloat(val)
		if err != nil {
			t.Errorf("FormatJCSFloat(0x%016x) failed: %v", entry.bits, err)
			continue
		}
		if got != entry.expected {
			t.Errorf("bits 0x%016x (%s): got %q, want %q", entry.bits, entry.comment, got, entry.expected)
		}
	}
}

func TestJCS_IEEE754_BigIntPrecisionLoss(t *testing.T) {
	// 9007199254740993 (2^53 + 1) cannot be represented in IEEE-754 float64.
	// RFC 8785 mandates IEEE-754 semantics: it must round to 9007199254740992.
	input := map[string]json.Number{
		"large": json.Number("9007199254740993"),
	}
	out, err := CanonicalJSON(input)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	expected := `{"large":9007199254740992}`
	if string(out) != expected {
		t.Errorf("got %s, want %s", string(out), expected)
	}
}

func TestJCS_StructWithInvalidUTF8Rejected(t *testing.T) {
	type SampleStruct struct {
		Name  string
		Value string
	}
	bad := SampleStruct{
		Name:  "valid",
		Value: "\xed\xa0\x80", // lone surrogate U+D800 in CESU-8
	}
	_, err := CanonicalJSON(bad)
	if err == nil {
		t.Fatalf("expected error for struct field with lone surrogate, got nil")
	}
}

