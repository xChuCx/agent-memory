package cli

import (
	"fmt"
	"strings"
)

// Minimal dependency-free unified diff, used by `review --diff` to show what
// applying a staged proposal would change against the current on-disk file.
// Line-level LCS + standard unified-diff hunking with a few lines of context.
// Memory files are small, so the O(n·m) LCS table is never a concern.

const diffContext = 3

// diffOp is one line in the LCS alignment: kind is ' ' (common), '-'
// (only in old), or '+' (only in new).
type diffOp struct {
	kind byte
	text string
}

// splitDiffLines splits text into lines, dropping the single empty element a
// trailing newline produces so a newline-terminated file isn't reported as
// having an extra blank line.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// diffLines returns an LCS-based alignment of a → b.
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// lcs[i][j] = length of the longest common subsequence of a[i:] and b[j:].
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// unifiedDiff renders an LCS line diff of old→new as unified-diff text with
// diffContext lines of context. Returns "" when old and new are identical.
func unifiedDiff(oldText, newText, oldName, newName string) string {
	ops := diffLines(splitDiffLines(oldText), splitDiffLines(newText))

	// Mark which ops to keep: every change, plus diffContext common lines on
	// each side of a change run.
	keep := make([]bool, len(ops))
	for idx, o := range ops {
		if o.kind == ' ' {
			continue
		}
		lo := idx - diffContext
		if lo < 0 {
			lo = 0
		}
		hi := idx + diffContext
		if hi >= len(ops) {
			hi = len(ops) - 1
		}
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}

	var out strings.Builder
	out.WriteString("--- " + oldName + "\n")
	out.WriteString("+++ " + newName + "\n")

	// Walk ops, emitting a hunk for each maximal run of kept ops. aLine/bLine
	// track the 1-based line number of the NEXT line in each file.
	aLine, bLine := 1, 1
	emittedAny := false
	idx := 0
	for idx < len(ops) {
		if !keep[idx] {
			if ops[idx].kind != '+' {
				aLine++
			}
			if ops[idx].kind != '-' {
				bLine++
			}
			idx++
			continue
		}
		// Start a hunk at idx.
		aStart, bStart := aLine, bLine
		var aCount, bCount int
		var body strings.Builder
		for idx < len(ops) && keep[idx] {
			switch ops[idx].kind {
			case ' ':
				body.WriteString(" " + ops[idx].text + "\n")
				aCount++
				bCount++
				aLine++
				bLine++
			case '-':
				body.WriteString("-" + ops[idx].text + "\n")
				aCount++
				aLine++
			case '+':
				body.WriteString("+" + ops[idx].text + "\n")
				bCount++
				bLine++
			}
			idx++
		}
		// A side with zero lines uses the line-before as its start (GNU style:
		// "-1,0" for an insertion after line 1).
		ha, hb := aStart, bStart
		if aCount == 0 {
			ha = aStart - 1
		}
		if bCount == 0 {
			hb = bStart - 1
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n", ha, aCount, hb, bCount)
		out.WriteString(body.String())
		emittedAny = true
	}
	if !emittedAny {
		return ""
	}
	return out.String()
}
