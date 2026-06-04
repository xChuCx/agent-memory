package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func cpWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cpRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyDirValidated_CopiesTree(t *testing.T) {
	src := t.TempDir()
	cpWrite(t, filepath.Join(src, "a.md"), "A")
	cpWrite(t, filepath.Join(src, "sub", "b.md"), "B")
	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyDirValidated(src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got := cpRead(t, filepath.Join(dst, "a.md")); got != "A" {
		t.Errorf("a.md = %q", got)
	}
	if got := cpRead(t, filepath.Join(dst, "sub", "b.md")); got != "B" {
		t.Errorf("sub/b.md = %q", got)
	}
}

func TestCopyDirValidated_RejectsSymlink(t *testing.T) {
	src := t.TempDir()
	cpWrite(t, filepath.Join(src, "real.md"), "x")
	if err := os.Symlink(filepath.Join(src, "real.md"), filepath.Join(src, "link.md")); err != nil {
		t.Skip("symlink unsupported on this platform: " + err.Error())
	}
	if err := CopyDirValidated(src, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestSwapDir_ReplacesAtomically(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "store")
	cpWrite(t, filepath.Join(dest, "old.md"), "old")
	staging := filepath.Join(root, "store.tmp")
	cpWrite(t, filepath.Join(staging, "new.md"), "new")

	if err := SwapDir(staging, dest); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if PathExists(filepath.Join(dest, "old.md")) {
		t.Error("old content should be gone after swap")
	}
	if got := cpRead(t, filepath.Join(dest, "new.md")); got != "new" {
		t.Errorf("new.md = %q", got)
	}
	if PathExists(staging) {
		t.Error("staging should be consumed by swap")
	}
}

func TestSwapDir_IntoEmptyDest(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "store") // does not exist yet
	staging := filepath.Join(root, "store.tmp")
	cpWrite(t, filepath.Join(staging, "x.md"), "x")
	if err := SwapDir(staging, dest); err != nil {
		t.Fatalf("swap into empty: %v", err)
	}
	if got := cpRead(t, filepath.Join(dest, "x.md")); got != "x" {
		t.Errorf("x.md = %q", got)
	}
}
