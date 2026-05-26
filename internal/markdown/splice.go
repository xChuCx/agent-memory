package markdown

import (
	"fmt"
	"sort"
)

// SpliceOp describes a single byte-range substitution: replace src[ByteStart:ByteEnd]
// with Replacement. ByteEnd is exclusive. Replacement may be empty (delete)
// or longer than the original range (insert + replace).
type SpliceOp struct {
	ByteStart   int
	ByteEnd     int
	Replacement []byte
}

// Splice applies one or more SpliceOps to src and returns the new bytes.
//
// All ByteStart/ByteEnd values refer to the ORIGINAL src; the function
// internally sorts ops by ByteStart and stitches the result piece by piece,
// so callers don't have to worry about offsets shifting between ops.
//
// Returns an error if:
//   - any op has invalid bounds (ByteStart < 0, ByteEnd > len(src),
//     ByteStart > ByteEnd);
//   - any two ops overlap (a previous op's ByteEnd > the next op's ByteStart
//     after sorting).
//
// An empty ops slice returns a copy of src and no error.
func Splice(src []byte, ops []SpliceOp) ([]byte, error) {
	if len(ops) == 0 {
		out := make([]byte, len(src))
		copy(out, src)
		return out, nil
	}

	sorted := make([]SpliceOp, len(ops))
	copy(sorted, ops)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ByteStart < sorted[j].ByteStart
	})

	if err := validateOps(src, sorted); err != nil {
		return nil, err
	}

	// Build the result piece by piece: prefix, [replacement, gap]*, suffix.
	resultLen := len(src)
	for _, op := range sorted {
		resultLen += len(op.Replacement) - (op.ByteEnd - op.ByteStart)
	}
	if resultLen < 0 {
		resultLen = 0
	}
	out := make([]byte, 0, resultLen)

	cursor := 0
	for _, op := range sorted {
		out = append(out, src[cursor:op.ByteStart]...)
		out = append(out, op.Replacement...)
		cursor = op.ByteEnd
	}
	out = append(out, src[cursor:]...)
	return out, nil
}

// validateOps assumes ops is already sorted ascending by ByteStart.
func validateOps(src []byte, ops []SpliceOp) error {
	for i, op := range ops {
		if op.ByteStart < 0 {
			return fmt.Errorf("splice op %d: negative ByteStart %d", i, op.ByteStart)
		}
		if op.ByteEnd > len(src) {
			return fmt.Errorf("splice op %d: ByteEnd %d exceeds src length %d", i, op.ByteEnd, len(src))
		}
		if op.ByteStart > op.ByteEnd {
			return fmt.Errorf("splice op %d: ByteStart %d > ByteEnd %d", i, op.ByteStart, op.ByteEnd)
		}
		if i > 0 && ops[i-1].ByteEnd > op.ByteStart {
			return fmt.Errorf("splice op %d overlaps previous op: [%d,%d) ∩ [%d,%d)",
				i, ops[i-1].ByteStart, ops[i-1].ByteEnd, op.ByteStart, op.ByteEnd)
		}
	}
	return nil
}

// ReplaceSection is a convenience wrapper that applies one Splice op covering
// the section's full range. Equivalent to:
//
//	Splice(src, []SpliceOp{{ByteStart: sec.ByteStart, ByteEnd: sec.ByteEnd, Replacement: newContent}})
func ReplaceSection(src []byte, sec Section, newContent []byte) ([]byte, error) {
	return Splice(src, []SpliceOp{{
		ByteStart:   sec.ByteStart,
		ByteEnd:     sec.ByteEnd,
		Replacement: newContent,
	}})
}
