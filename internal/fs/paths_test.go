package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMemoryPath_Accepts(t *testing.T) {
	root := t.TempDir() // already absolute
	cases := map[string]string{
		"simple file":           "decisions.md",
		"nested module":         "modules/auth.md",
		"archive":               "archive/2026-05-foo.md",
		"local branch":          "local/current.feature-x.md",
		"forward slashes":       "modules/sub/file.md",
		"with single dot":       "./decisions.md",
		"manifest YAML allowed": "meta/manifest.yaml",
		"schema YAML allowed":   "meta/schema.yaml",
		"index.md allowed":      "index.md",
	}
	for name, rel := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ValidateMemoryPath(root, rel)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasPrefix(got, root) {
				t.Errorf("result %q does not start with root %q", got, root)
			}
		})
	}
}

func TestValidateMemoryPath_Rejects(t *testing.T) {
	root := t.TempDir()
	cases := map[string]string{
		"empty":                   "",
		"absolute":                filepath.Join(root, "x.md"),
		"parent direct":           "..",
		"parent dir":              "../etc/passwd",
		"deep parent":             "../../etc/passwd",
		"embedded parent":         "modules/../../etc/passwd",
		"only dotdot in middle":   "modules/../foo.md", // resolves to foo.md, ALLOWED
		"derived sqlite":          "meta/index.sqlite",
		"derived sqlite wal":      "meta/index.sqlite-wal",
		"derived sqlite shm":      "meta/index.sqlite-shm",
		"derived sqlite journal":  "meta/index.sqlite-journal",
		"derived lock":            "meta/lock",
	}
	// The "only dotdot in middle" case resolves cleanly to "foo.md" and is
	// allowed — adjust expectations.
	allowedExceptions := map[string]bool{
		"only dotdot in middle": true,
	}
	for name, rel := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateMemoryPath(root, rel)
			if allowedExceptions[name] {
				if err != nil {
					t.Errorf("expected acceptance for %q, got error: %v", rel, err)
				}
				return
			}
			if err == nil {
				t.Errorf("expected error for %q, got nil", rel)
			}
		})
	}
}

func TestValidateMemoryPath_RootMustBeAbsolute(t *testing.T) {
	_, err := ValidateMemoryPath("relative/root", "x.md")
	if err == nil {
		t.Error("expected error for relative root")
	}
}

func TestValidateMemoryPath_PreservesOSSeparator(t *testing.T) {
	root := t.TempDir()
	got, err := ValidateMemoryPath(root, "modules/auth.md")
	if err != nil {
		t.Fatal(err)
	}
	// Result should join with OS separator.
	want := filepath.Join(root, "modules", "auth.md")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsDerivedPath(t *testing.T) {
	cases := map[string]bool{
		"meta/index.sqlite":         true,
		"meta/index.sqlite-wal":     true,
		"meta/index.sqlite-shm":     true,
		"meta/index.sqlite-journal": true,
		"meta/lock":                 true,
		"meta/manifest.yaml":        false,
		"meta/schema.yaml":          false,
		"index.md":                  false,
		"modules/auth.md":           false,
		"archive/old.md":            false,
		"local/current.main.md":     false,
		"":                          false, // empty isn't derived; ValidateMemoryPath catches it
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			if got := IsDerivedPath(path); got != want {
				t.Errorf("IsDerivedPath(%q) = %v, want %v", path, got, want)
			}
		})
	}
}

func TestValidateMemoryPath_SymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()

	symlinkPath := filepath.Join(root, "modules")
	err := os.Symlink(outsideDir, symlinkPath)
	if err != nil {
		t.Skipf("skipping symlink test: symlink creation not permitted: %v", err)
	}

	// Attempt to validate a path traversing the external symlink with non-existent leaf.
	_, err = ValidateMemoryPath(root, "modules/auth.md")
	if err == nil {
		t.Fatalf("expected error for symlink escaping root, got nil")
	}
	if !strings.Contains(err.Error(), "escapes root via symlink") {
		t.Errorf("expected 'escapes root via symlink' error, got: %v", err)
	}
}

func TestValidateMemoryPath_RootPrefixCollision(t *testing.T) {
	tempBase := t.TempDir()
	root := filepath.Join(tempBase, "memory")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	evilDir := filepath.Join(tempBase, "memory-evil")
	if err := os.MkdirAll(evilDir, 0755); err != nil {
		t.Fatal(err)
	}

	// IsSubpath must reject memory-evil against memory
	if IsSubpath(root, filepath.Join(evilDir, "secret.md")) {
		t.Errorf("IsSubpath allowed memory-evil to match memory prefix")
	}
	if !IsSubpath(root, filepath.Join(root, "valid.md")) {
		t.Errorf("IsSubpath rejected legitimate child of memory")
	}
}

func TestValidateMemoryPath_NestedParentSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	parent := filepath.Join(root, "sub", "level1")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}

	symlink := filepath.Join(parent, "escaped")
	if err := os.Symlink(outside, symlink); err != nil {
		t.Skipf("skipping symlink test: %v", err)
	}

	_, err := ValidateMemoryPath(root, "sub/level1/escaped/deep/file.md")
	if err == nil {
		t.Fatalf("expected error for nested symlink escaping root, got nil")
	}
	if !strings.Contains(err.Error(), "escapes root via symlink") {
		t.Errorf("expected escapes root via symlink, got: %v", err)
	}
}

