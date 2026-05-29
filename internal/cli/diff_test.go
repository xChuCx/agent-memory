package cli

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_Identical(t *testing.T) {
	if d := unifiedDiff("a\nb\n", "a\nb\n", "a/x", "b/x"); d != "" {
		t.Errorf("identical inputs should diff to empty, got:\n%s", d)
	}
}

func TestUnifiedDiff_SingleLineChange(t *testing.T) {
	got := unifiedDiff("a\nb\nc\n", "a\nB\nc\n", "a/x", "b/x")
	want := "--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if got != want {
		t.Errorf("unifiedDiff mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestUnifiedDiff_PureAddition(t *testing.T) {
	got := unifiedDiff("", "x\ny\n", "a/new", "b/new")
	want := "--- a/new\n+++ b/new\n@@ -0,0 +1,2 @@\n+x\n+y\n"
	if got != want {
		t.Errorf("pure-addition diff mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestUnifiedDiff_ContextWindow(t *testing.T) {
	// A change deep inside a long file: only nearby context lines appear,
	// far-away unchanged lines are omitted (no leading "l1").
	var old, neu strings.Builder
	for i := 1; i <= 12; i++ {
		old.WriteString("l")
		neu.WriteString("l")
		old.WriteByte(byte('0' + i%10))
		neu.WriteByte(byte('0' + i%10))
		old.WriteByte('\n')
		neu.WriteByte('\n')
	}
	// Mutate line 7 in the new text.
	n := strings.Replace(neu.String(), "l7\n", "lSEVEN\n", 1)
	got := unifiedDiff(old.String(), n, "a/x", "b/x")
	if !strings.Contains(got, "-l7") || !strings.Contains(got, "+lSEVEN") {
		t.Errorf("expected the line-7 change, got:\n%s", got)
	}
	if strings.Contains(got, " l1\n") {
		t.Errorf("line 1 is outside the context window and should be omitted:\n%s", got)
	}
	if !strings.HasPrefix(got, "--- a/x\n+++ b/x\n@@ ") {
		t.Errorf("missing unified-diff header:\n%s", got)
	}
}

// TestReviewDiff_ShowsStagedChange exercises `review --diff` end to end via
// runReviewDetail: a staged decision should diff against the on-disk
// decisions.md as additions.
func TestReviewDiff_ShowsStagedChange(t *testing.T) {
	root, id := stagingFixture(t)
	d, err := runReviewDetail(root, id, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Files != nil {
		t.Errorf("Files should be nil without --show, got %v", d.Files)
	}
	diff, ok := d.Diffs["decisions.md"]
	if !ok {
		t.Fatalf("decisions.md missing from Diffs: %v", d.Diffs)
	}
	if !strings.Contains(diff, "@@") {
		t.Errorf("diff has no hunk header:\n%s", diff)
	}
	if !strings.Contains(diff, "+") || !strings.Contains(diff, "cli-test") {
		t.Errorf("diff should show the staged decision as additions:\n%s", diff)
	}
}
