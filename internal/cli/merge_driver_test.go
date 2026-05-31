package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mdBase = `# Decisions
<!-- @id: decisions -->

## Use Postgres
<!-- @id: postgres -->

Transactional storage.
`

// write3 lays down base/ours/theirs temp files and returns their paths.
func write3(t *testing.T, base, ours, theirs string) (string, string, string) {
	t.Helper()
	d := t.TempDir()
	p := func(name, body string) string {
		full := filepath.Join(d, name)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}
	return p("base", base), p("ours", ours), p("theirs", theirs)
}

func TestRunMergeDriver_CleanUnion(t *testing.T) {
	ours := mdBase + "\n## Use Kafka\n<!-- @id: kafka -->\n\nStreaming.\n"
	theirs := mdBase + "\n## Use Redis\n<!-- @id: redis -->\n\nCache.\n"
	bp, op, tp := write3(t, mdBase, ours, theirs)

	if err := runMergeDriver(bp, op, tp); err != nil {
		t.Fatalf("clean union should not error: %v", err)
	}
	got, _ := os.ReadFile(op) // driver writes the result to the ours path
	s := string(got)
	if !strings.Contains(s, "@id: kafka") || !strings.Contains(s, "@id: redis") {
		t.Errorf("merged ours file missing a section:\n%s", s)
	}
	if strings.Contains(s, "<<<<<<<") || strings.Contains(s, "@merge-conflict") {
		t.Errorf("unexpected conflict markers:\n%s", s)
	}
}

func TestRunMergeDriver_ConflictExitsNonZero(t *testing.T) {
	ours := strings.Replace(mdBase, "Transactional storage.", "Picked for JSONB.", 1)
	theirs := strings.Replace(mdBase, "Transactional storage.", "Picked for transactions.", 1)
	bp, op, tp := write3(t, mdBase, ours, theirs)

	err := runMergeDriver(bp, op, tp)
	if err == nil {
		t.Fatal("divergent edits must return an error (non-zero exit for git)")
	}
	got, _ := os.ReadFile(op)
	if !strings.Contains(string(got), "@merge-conflict") {
		t.Errorf("conflict block not written to ours file:\n%s", got)
	}
}

func TestEnsureLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".gitattributes")

	if err := ensureLine(p, "*.md merge=agent-memory"); err != nil {
		t.Fatal(err)
	}
	if err := ensureLine(p, "*.md merge=agent-memory"); err != nil { // idempotent
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if n := strings.Count(string(b), "*.md merge=agent-memory"); n != 1 {
		t.Errorf("line written %d times, want 1:\n%s", n, b)
	}

	// Appends alongside an existing unrelated line.
	p2 := filepath.Join(t.TempDir(), ".gitattributes")
	_ = os.WriteFile(p2, []byte("*.txt text\n"), 0o644)
	if err := ensureLine(p2, "*.md merge=agent-memory"); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(p2)
	if !strings.Contains(string(b2), "*.txt text") || !strings.Contains(string(b2), "*.md merge=agent-memory") {
		t.Errorf("ensureLine clobbered existing content:\n%s", b2)
	}
}
