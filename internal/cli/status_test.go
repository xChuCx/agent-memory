package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatus_AfterInit_AllCountsZero(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "fresh"}); err != nil {
		t.Fatal(err)
	}

	r, err := runStatus(dir)
	if err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	if r.Repo != "fresh" {
		t.Errorf("Repo = %q, want fresh", r.Repo)
	}
	if r.Version == "" {
		t.Error("Version is empty")
	}
	// Right after init: index.md, conventions.md, decisions.md, pitfalls.md
	// each map to a single category. Other categories are at 0.
	expected := map[string]int{
		"index":       1,
		"conventions": 1,
		"decisions":   1,
		"pitfalls":    1,
		"modules":     0,
		"archive":     0,
		"current":     0,
		"sessions":    0,
	}
	for name, want := range expected {
		if got := r.Categories[name]; got != want {
			t.Errorf("Categories[%q] = %d, want %d", name, got, want)
		}
	}
}

func TestStatus_CountsModuleFiles(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	// Add some module files.
	modulesDir := filepath.Join(dir, ".agent-memory", "modules")
	for _, name := range []string{"auth.md", "payments.md", "search.md"} {
		if err := os.WriteFile(filepath.Join(modulesDir, name), []byte("# x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	r, err := runStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Categories["modules"] != 3 {
		t.Errorf("modules count = %d, want 3", r.Categories["modules"])
	}
}

func TestStatus_RejectsMissingAgentMemory(t *testing.T) {
	dir := t.TempDir()
	_, err := runStatus(dir)
	if err == nil {
		t.Fatal("expected error for missing .agent-memory/")
	}
	if !strings.Contains(err.Error(), ".agent-memory") {
		t.Errorf("error doesn't mention .agent-memory: %v", err)
	}
}

func TestStatus_JSONOutputIsValid(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "json-test"}); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"status", "--root", dir, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("status --json: %v\noutput: %s", err, out.String())
	}

	var r StatusReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, out.String())
	}
	if r.Repo != "json-test" {
		t.Errorf("Repo = %q, want json-test", r.Repo)
	}
	if r.Categories == nil {
		t.Error("Categories is nil in JSON output")
	}
	if r.ManifestPath == "" {
		t.Error("ManifestPath is empty")
	}
	if r.SchemaPath == "" {
		t.Error("SchemaPath is empty")
	}
}

func TestStatus_HumanOutputMentionsCategories(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "human"}); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"status", "--root", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("status: %v", err)
	}
	output := out.String()
	for _, want := range []string{"agent-memory ", "repo: human", "Categories:", "decisions"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q\noutput:\n%s", want, output)
		}
	}
}

func TestStatus_NoLockMetadataAfterInit(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	r, err := runStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Lock.OwnerID != "" {
		t.Errorf("expected empty lock metadata after init, got OwnerID=%q", r.Lock.OwnerID)
	}
}
