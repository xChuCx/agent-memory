package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// runInitViaCobra exercises the full cobra wiring instead of calling
// runInit() directly. Used as the integration smoke for at least one
// happy-path test; other tests can call runInit() directly for speed
// and clearer assertions.
func runInitViaCobra(t *testing.T, args []string) (string, error) {
	t.Helper()
	out := &bytes.Buffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestInit_HappyPath(t *testing.T) {
	dir := t.TempDir()
	output, err := runInitViaCobra(t, []string{"init", "--root", dir, "--name", "test-project"})
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, output)
	}

	memDir := filepath.Join(dir, ".agent-memory")

	// Required regular files.
	for _, rel := range []string{
		"meta/manifest.yaml",
		"meta/schema.yaml",
		".gitignore",
		"index.md",
		"conventions.md",
		"decisions.md",
		"pitfalls.md",
		"meta/lock",
		"modules/.gitkeep",
		"archive/.gitkeep",
	} {
		t.Run("file/"+rel, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(memDir, rel)); err != nil {
				t.Errorf("missing: %s (%v)", rel, err)
			}
		})
	}

	// Required directories.
	for _, rel := range []string{"modules", "archive", "local", "sessions", "staging", "meta"} {
		t.Run("dir/"+rel, func(t *testing.T) {
			info, err := os.Stat(filepath.Join(memDir, rel))
			if err != nil {
				t.Errorf("missing dir: %s (%v)", rel, err)
				return
			}
			if !info.IsDir() {
				t.Errorf("%s exists but is not a directory", rel)
			}
		})
	}

	// Manifest loads + has the requested project name.
	m, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Project.Name != "test-project" {
		t.Errorf("Project.Name = %q, want test-project", m.Project.Name)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("manifest doesn't validate: %v", err)
	}

	// Schema loads + validates.
	s, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("schema doesn't validate: %v", err)
	}

	// .gitignore includes the lock sidecar (per the fix in commit 145fd8e).
	gi, _ := os.ReadFile(filepath.Join(memDir, ".gitignore"))
	if !strings.Contains(string(gi), "meta/lock") {
		t.Errorf(".gitignore missing meta/lock entry")
	}
	if !strings.Contains(string(gi), "meta/lock.info") {
		t.Errorf(".gitignore missing meta/lock.info entry")
	}
	if !strings.Contains(string(gi), "local/") {
		t.Errorf(".gitignore missing local/ entry")
	}

	// Success message points at next steps.
	if !strings.Contains(output, "Initialized .agent-memory/") {
		t.Errorf("expected success message, got: %s", output)
	}
}

func TestInit_DefaultsProjectNameToBasename(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "my-cool-repo")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := runInit(io.Discard, initOptions{Root: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	m, err := config.LoadManifest(filepath.Join(dir, ".agent-memory", "meta", "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Project.Name != "my-cool-repo" {
		t.Errorf("Project.Name = %q, want my-cool-repo", m.Project.Name)
	}
}

func TestInit_RefusesIfExistsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "first"}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "second"})
	if err == nil {
		t.Fatal("expected error on re-init without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error doesn't mention 'already exists': %v", err)
	}
	// First project name must survive.
	m, _ := config.LoadManifest(filepath.Join(dir, ".agent-memory", "meta", "manifest.yaml"))
	if m.Project.Name != "first" {
		t.Errorf("Project.Name = %q, want first (untouched)", m.Project.Name)
	}
}

func TestInit_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "first"}); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "second", Force: true}); err != nil {
		t.Fatalf("forced re-init: %v", err)
	}
	m, _ := config.LoadManifest(filepath.Join(dir, ".agent-memory", "meta", "manifest.yaml"))
	if m.Project.Name != "second" {
		t.Errorf("Project.Name = %q, want second", m.Project.Name)
	}
}

func TestInit_WithMergeDriverPrintsNotice(t *testing.T) {
	dir := t.TempDir()
	out := &bytes.Buffer{}
	if err := runInit(out, initOptions{Root: dir, ProjectName: "p", WithMergeDriver: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "merge driver") {
		t.Errorf("expected merge-driver notice, got: %s", out.String())
	}
	// No .gitattributes should land in M1.
	if _, err := os.Stat(filepath.Join(dir, ".gitattributes")); err == nil {
		t.Error("init wrote .gitattributes in M1; merge driver setup is M7")
	}
}

func TestInit_DurableFilesAreValidMarkdown(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	memDir := filepath.Join(dir, ".agent-memory")
	for _, rel := range []string{"index.md", "conventions.md", "decisions.md", "pitfalls.md"} {
		b, err := os.ReadFile(filepath.Join(memDir, rel))
		if err != nil {
			t.Errorf("read %s: %v", rel, err)
			continue
		}
		if len(b) == 0 {
			t.Errorf("%s is empty", rel)
		}
		if !strings.HasPrefix(string(b), "# ") {
			t.Errorf("%s doesn't start with an H1 heading", rel)
		}
	}
}
