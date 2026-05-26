package markdown

import (
	"bytes"
	"testing"
)

func TestSplice_Empty(t *testing.T) {
	src := []byte("hello")
	got, err := Splice(src, nil)
	if err != nil {
		t.Fatalf("Splice(nil): %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Errorf("expected copy of src, got %q", got)
	}
	// Result must be a copy, not a shared slice.
	got[0] = 'X'
	if src[0] == 'X' {
		t.Error("Splice returned a slice that aliases src")
	}
}

func TestSplice_SingleOp(t *testing.T) {
	src := []byte("0123456789")
	ops := []SpliceOp{{ByteStart: 2, ByteEnd: 4, Replacement: []byte("AA")}}
	got, err := Splice(src, ops)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	want := []byte("01AA456789")
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSplice_MultiOp_Ascending(t *testing.T) {
	src := []byte("0123456789")
	ops := []SpliceOp{
		{ByteStart: 2, ByteEnd: 4, Replacement: []byte("AA")},
		{ByteStart: 6, ByteEnd: 8, Replacement: []byte("BB")},
	}
	got, err := Splice(src, ops)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	want := []byte("01AA45BB89")
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSplice_MultiOp_OutOfOrder(t *testing.T) {
	// Same as Ascending but ops are passed in reverse order. Splice must sort.
	src := []byte("0123456789")
	ops := []SpliceOp{
		{ByteStart: 6, ByteEnd: 8, Replacement: []byte("BB")},
		{ByteStart: 2, ByteEnd: 4, Replacement: []byte("AA")},
	}
	got, err := Splice(src, ops)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	want := []byte("01AA45BB89")
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSplice_InsertOnly(t *testing.T) {
	// Replacement longer than the source range = effective insert.
	src := []byte("ab")
	ops := []SpliceOp{{ByteStart: 1, ByteEnd: 1, Replacement: []byte("XYZ")}}
	got, err := Splice(src, ops)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Equal(got, []byte("aXYZb")) {
		t.Errorf("got %q, want %q", got, "aXYZb")
	}
}

func TestSplice_DeleteOnly(t *testing.T) {
	// Empty replacement = effective delete.
	src := []byte("hello world")
	ops := []SpliceOp{{ByteStart: 5, ByteEnd: 6, Replacement: nil}}
	got, err := Splice(src, ops)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	if !bytes.Equal(got, []byte("helloworld")) {
		t.Errorf("got %q, want %q", got, "helloworld")
	}
}

func TestSplice_EdgesOfFile(t *testing.T) {
	src := []byte("middle")
	// Replace prefix.
	got, err := Splice(src, []SpliceOp{{ByteStart: 0, ByteEnd: 3, Replacement: []byte("X")}})
	if err != nil {
		t.Fatalf("prefix: %v", err)
	}
	if !bytes.Equal(got, []byte("Xdle")) {
		t.Errorf("prefix replace: got %q, want %q", got, "Xdle")
	}
	// Replace suffix.
	got, err = Splice(src, []SpliceOp{{ByteStart: 3, ByteEnd: 6, Replacement: []byte("Y")}})
	if err != nil {
		t.Fatalf("suffix: %v", err)
	}
	if !bytes.Equal(got, []byte("midY")) {
		t.Errorf("suffix replace: got %q, want %q", got, "midY")
	}
}

func TestSplice_RejectInvalidRange(t *testing.T) {
	src := []byte("hello")
	cases := []struct {
		name string
		op   SpliceOp
	}{
		{"negative start", SpliceOp{ByteStart: -1, ByteEnd: 2}},
		{"end past EOF", SpliceOp{ByteStart: 0, ByteEnd: len(src) + 1}},
		{"start > end", SpliceOp{ByteStart: 3, ByteEnd: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Splice(src, []SpliceOp{c.op}); err == nil {
				t.Errorf("expected error for %+v", c.op)
			}
		})
	}
}

func TestSplice_RejectOverlap(t *testing.T) {
	src := []byte("0123456789")
	ops := []SpliceOp{
		{ByteStart: 2, ByteEnd: 6, Replacement: []byte("A")},
		{ByteStart: 4, ByteEnd: 8, Replacement: []byte("B")},
	}
	if _, err := Splice(src, ops); err == nil {
		t.Error("expected overlap error")
	}
}

func TestSplice_AdjacentOpsAllowed(t *testing.T) {
	// op1 ends exactly where op2 starts — not an overlap.
	src := []byte("0123456789")
	ops := []SpliceOp{
		{ByteStart: 2, ByteEnd: 4, Replacement: []byte("AA")},
		{ByteStart: 4, ByteEnd: 6, Replacement: []byte("BB")},
	}
	got, err := Splice(src, ops)
	if err != nil {
		t.Fatalf("adjacent ops should be allowed: %v", err)
	}
	if !bytes.Equal(got, []byte("01AABB6789")) {
		t.Errorf("got %q, want %q", got, "01AABB6789")
	}
}

func TestReplaceSection_DelegatesToSplice(t *testing.T) {
	src := []byte("0123456789")
	sec := Section{ByteStart: 2, ByteEnd: 5}
	got, err := ReplaceSection(src, sec, []byte("XYZ"))
	if err != nil {
		t.Fatalf("ReplaceSection: %v", err)
	}
	if !bytes.Equal(got, []byte("01XYZ56789")) {
		t.Errorf("got %q, want %q", got, "01XYZ56789")
	}
}
