package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// requireGit skips the test if `git` is not on PATH. CI matrices for all
// three OSes ship git pre-installed, so this is mostly belt-and-suspenders
// for unusual local setups.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "init", "-q"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestActiveBranch_NotGitRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	info, err := ActiveBranch(dir)
	if err != nil {
		t.Fatalf("ActiveBranch: %v", err)
	}
	if info.IsGitRepo {
		t.Errorf("IsGitRepo = true for non-repo")
	}
	if info.Name != "" || info.ShortSHA != "" {
		t.Errorf("expected zero info for non-repo, got %+v", info)
	}
}

func TestActiveBranch_OnMain(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	info, err := ActiveBranch(dir)
	if err != nil {
		t.Fatalf("ActiveBranch: %v", err)
	}
	if !info.IsGitRepo {
		t.Error("IsGitRepo = false after git init")
	}
	if info.Name != "main" {
		t.Errorf("Name = %q, want main", info.Name)
	}
	if info.IsDetached {
		t.Error("IsDetached = true on a normal branch")
	}
}

func TestActiveBranch_OnFeatureBranch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	out, err := exec.Command("git", "-C", dir, "checkout", "-q", "-b", "feature/auth").CombinedOutput()
	if err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
	info, err := ActiveBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "feature/auth" {
		t.Errorf("Name = %q, want feature/auth", info.Name)
	}
	if info.IsDetached {
		t.Error("IsDetached should be false")
	}
}

func TestActiveBranch_DetachedHEAD(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Add a second commit so we have something to detach to.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "f.txt")
	mustGit(t, dir, "commit", "-q", "-m", "second")
	mustGit(t, dir, "checkout", "-q", "--detach")

	info, err := ActiveBranch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDetached {
		t.Error("IsDetached should be true")
	}
	if info.Name != "" {
		t.Errorf("Name = %q on detached HEAD, want empty", info.Name)
	}
	if len(info.ShortSHA) < 4 || len(info.ShortSHA) > 12 {
		t.Errorf("ShortSHA = %q, want 4-12 chars", info.ShortSHA)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestSlugBranch(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain main", "main", "main"},
		{"feature slash", "feature/auth-rotation", "feature-auth-rotation"},
		{"upper to lower", "Bugfix/JIRA-123", "bugfix-jira-123"},
		{"version dots", "release/2026.05.01", "release-2026-05-01"},
		{"empty", "", ""},
		{"only punctuation", "//", ""},
		{"unicode stripped", "feature/привет", "feature"},
		{"leading slashes", "///main", "main"},
		{"trailing slashes", "main///", "main"},
		{"consecutive separators", "foo//bar", "foo-bar"},
		{"single char", "x", "x"},
		{"digits only", "12345", "12345"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SlugBranch(c.in)
			if got != c.want {
				t.Errorf("SlugBranch(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
