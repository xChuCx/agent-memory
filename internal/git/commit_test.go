package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo bootstraps a fresh git repo in t.TempDir() configured with a
// throwaway author identity so `git commit` doesn't error on a runner
// with no global config. Returns the absolute root path.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	root := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	mustRun("init", "-q")
	mustRun("config", "user.email", "test@example.com")
	mustRun("config", "user.name", "Test")
	// Make an initial empty commit so HEAD exists. Otherwise the
	// `git diff --cached` we use to detect staged changes errors
	// because there's no HEAD to diff against.
	mustRun("commit", "--allow-empty", "-m", "init", "-q")
	return root
}

// writeFile creates root/rel with body and returns the relative path.
func writeFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return rel
}

// headSubject returns the first line of HEAD's commit message.
func headSubject(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// stagedFiles returns the relative paths currently in the index (staged
// but not committed).
func stagedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "diff", "--cached", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git diff --cached: %v", err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// =============================================================================
// AddPaths
// =============================================================================

func TestAddPaths_StagesProvidedFiles(t *testing.T) {
	root := initRepo(t)
	rel := writeFile(t, root, "decisions.md", "# d\n")

	got, err := AddPaths(root, []string{rel})
	if err != nil {
		t.Fatalf("AddPaths: %v", err)
	}
	if len(got) != 1 || got[0] != rel {
		t.Errorf("returned paths = %v, want [%s]", got, rel)
	}
	if staged := stagedFiles(t, root); len(staged) != 1 || staged[0] != rel {
		t.Errorf("staged files = %v, want [%s]", staged, rel)
	}
}

func TestAddPaths_EmptyPathsNoop(t *testing.T) {
	root := initRepo(t)
	got, err := AddPaths(root, nil)
	if err != nil || got != nil {
		t.Errorf("AddPaths(nil) = %v, %v; want (nil, nil)", got, err)
	}
}

func TestAddPaths_NotAGitRepoIsNoop(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "decisions.md", "# d\n")
	got, err := AddPaths(dir, []string{"decisions.md"})
	if err != nil {
		t.Errorf("AddPaths on non-repo errored: %v", err)
	}
	if got != nil {
		t.Errorf("returned paths = %v, want nil on non-repo", got)
	}
}

func TestAddPaths_MultipleFiles(t *testing.T) {
	root := initRepo(t)
	a := writeFile(t, root, "a.md", "a\n")
	b := writeFile(t, root, "sub/b.md", "b\n")
	if _, err := AddPaths(root, []string{a, b}); err != nil {
		t.Fatalf("AddPaths: %v", err)
	}
	staged := stagedFiles(t, root)
	if len(staged) != 2 {
		t.Errorf("staged = %v, want 2 entries", staged)
	}
}

// =============================================================================
// Commit
// =============================================================================

func TestCommit_CreatesCommit(t *testing.T) {
	root := initRepo(t)
	rel := writeFile(t, root, "decisions.md", "# d\n")
	if _, err := AddPaths(root, []string{rel}); err != nil {
		t.Fatal(err)
	}

	sha, err := Commit(root, "chore(memory): test commit")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(sha) < 7 {
		t.Errorf("sha = %q, want non-empty hex string", sha)
	}
	if got := headSubject(t, root); got != "chore(memory): test commit" {
		t.Errorf("subject = %q, want %q", got, "chore(memory): test commit")
	}
}

func TestCommit_NothingStagedIsNoop(t *testing.T) {
	root := initRepo(t)
	sha, err := Commit(root, "chore(memory): should-not-happen")
	if err != nil {
		t.Errorf("Commit with empty index errored: %v", err)
	}
	if sha != "" {
		t.Errorf("sha = %q, want empty (no-op)", sha)
	}
	// HEAD should still be the initial empty commit; subject is "init".
	if subj := headSubject(t, root); subj != "init" {
		t.Errorf("HEAD subject = %q, want init", subj)
	}
}

func TestCommit_NotAGitRepoIsNoop(t *testing.T) {
	dir := t.TempDir()
	sha, err := Commit(dir, "chore(memory): test")
	if err != nil {
		t.Errorf("Commit on non-repo errored: %v", err)
	}
	if sha != "" {
		t.Errorf("sha = %q, want empty on non-repo", sha)
	}
}

func TestCommit_MultilineMessage(t *testing.T) {
	root := initRepo(t)
	rel := writeFile(t, root, "x.md", "x\n")
	if _, err := AddPaths(root, []string{rel}); err != nil {
		t.Fatal(err)
	}
	msg := "chore(memory): apply 20260527T120000-test\n\nIntent: record_decision\nFiles: x.md\n"
	sha, err := Commit(root, msg)
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Error("sha empty after successful Commit")
	}
	if subj := headSubject(t, root); subj != "chore(memory): apply 20260527T120000-test" {
		t.Errorf("subject = %q, want only the first line", subj)
	}
}

// =============================================================================
// insideWorkTree + hasStagedChanges (covered indirectly above, plus one
// dedicated probe to guard against future regressions).
// =============================================================================

func TestInsideWorkTree(t *testing.T) {
	if insideWorkTree(t.TempDir()) {
		t.Error("empty dir reported as work tree")
	}
	root := initRepo(t)
	if !insideWorkTree(root) {
		t.Error("git repo not reported as work tree")
	}
}

func TestHasStagedChanges(t *testing.T) {
	root := initRepo(t)
	if hasStagedChanges(root) {
		t.Error("fresh repo has staged changes reported")
	}
	rel := writeFile(t, root, "y.md", "y\n")
	if _, err := AddPaths(root, []string{rel}); err != nil {
		t.Fatal(err)
	}
	if !hasStagedChanges(root) {
		t.Error("staged file not detected")
	}
}
