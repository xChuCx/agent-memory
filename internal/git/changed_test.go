package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChangedFiles_NotGitRepo(t *testing.T) {
	requireGit(t)
	got, err := ChangedFiles(t.TempDir())
	if err != nil {
		t.Fatalf("non-repo should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("non-repo ChangedFiles = %v, want empty", got)
	}
}

func TestChangedFiles_CleanTree(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir) // an --allow-empty commit; nothing else in the tree
	got, err := ChangedFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("clean tree ChangedFiles = %v, want empty", got)
	}
}

func TestChangedFiles_ModifiedAndUntracked(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Commit a tracked file, then modify it.
	tracked := filepath.Join(dir, "tracked.go")
	if err := os.WriteFile(tracked, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "tracked.go")
	mustGit(t, dir, "commit", "-q", "-m", "add tracked")
	if err := os.WriteFile(tracked, []byte("package x\n\nvar V = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create an untracked file in a subdirectory.
	sub := filepath.Join(dir, "internal", "feature")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "new.go"), []byte("package feature\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ChangedFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"tracked.go":              false,
		"internal/feature/new.go": false, // forward-slash even on Windows (git emits /)
	}
	for _, p := range got {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("expected %q in ChangedFiles, got %v", p, got)
		}
	}
}
