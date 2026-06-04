package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mkGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestClone_HeadCommit_Checkout(t *testing.T) {
	src := mkGitRepo(t)
	dst := filepath.Join(t.TempDir(), "clone")

	if err := Clone(src, dst); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if !IsWorkTree(dst) {
		t.Fatal("clone should be a git work tree")
	}
	commit, err := HeadCommit(dst)
	if err != nil {
		t.Fatalf("head commit: %v", err)
	}
	if len(commit) < 40 {
		t.Fatalf("commit sha looks too short: %q", commit)
	}
	if err := Checkout(dst, commit); err != nil {
		t.Fatalf("checkout %s: %v", commit, err)
	}
}

func TestClone_BadSourceErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dst := filepath.Join(t.TempDir(), "clone")
	if err := Clone(filepath.Join(t.TempDir(), "nope"), dst); err == nil {
		t.Fatal("expected clone of a nonexistent source to fail")
	}
}
