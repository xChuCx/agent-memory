package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- ApprovalMode ----------

func TestApprovalMode_IsValid(t *testing.T) {
	cases := map[ApprovalMode]bool{
		ApprovalApply:      true,
		ApprovalStage:      true,
		ApprovalServerOnly: true,
		"":                 false,
		"unknown":          false,
		"Apply":            false, // case sensitive
	}
	for m, want := range cases {
		t.Run(string(m), func(t *testing.T) {
			if got := m.IsValid(); got != want {
				t.Errorf("%q.IsValid() = %v, want %v", m, got, want)
			}
		})
	}
}

// ---------- DefaultSchema ----------

func TestDefaultSchema_HasExpectedCategories(t *testing.T) {
	s := DefaultSchema()
	expected := []string{
		"index", "conventions", "decisions", "pitfalls",
		"modules", "archive", "current", "sessions",
	}
	for _, name := range expected {
		if _, ok := s.Categories[name]; !ok {
			t.Errorf("missing category %q", name)
		}
	}
	if len(s.Categories) != len(expected) {
		t.Errorf("got %d categories, want %d", len(s.Categories), len(expected))
	}
}

func TestDefaultSchema_PassesValidate(t *testing.T) {
	s := DefaultSchema()
	if err := s.Validate(); err != nil {
		t.Errorf("default schema fails Validate: %v", err)
	}
}

func TestDefaultSchema_IndexIsServerOnly(t *testing.T) {
	s := DefaultSchema()
	idx := s.Categories["index"]
	if !idx.ServerManaged {
		t.Error("index should be ServerManaged")
	}
	if idx.AgentWritable {
		t.Error("index must not be AgentWritable")
	}
	if idx.Approval != ApprovalServerOnly {
		t.Errorf("index.Approval = %q, want %q", idx.Approval, ApprovalServerOnly)
	}
}

func TestDefaultSchema_LocalIsNotGitTracked(t *testing.T) {
	s := DefaultSchema()
	if s.Categories["current"].GitTracked {
		t.Error("current should NOT be GitTracked by default")
	}
	if s.Categories["sessions"].GitTracked {
		t.Error("sessions should NOT be GitTracked by default")
	}
}

// ---------- CategoryForPath ----------

func TestCategoryForPath_ExactFile(t *testing.T) {
	s := DefaultSchema()
	s.populateCategoryNames()
	cases := map[string]string{
		"index.md":       "index",
		"conventions.md": "conventions",
		"decisions.md":   "decisions",
		"pitfalls.md":    "pitfalls",
	}
	for path, wantName := range cases {
		t.Run(path, func(t *testing.T) {
			cat, ok := s.CategoryForPath(path)
			if !ok {
				t.Fatalf("%q not matched", path)
			}
			if cat.Name != wantName {
				t.Errorf("Name = %q, want %q", cat.Name, wantName)
			}
		})
	}
}

func TestCategoryForPath_Glob(t *testing.T) {
	s := DefaultSchema()
	s.populateCategoryNames()
	cases := map[string]string{
		"modules/auth.md":           "modules",
		"modules/payments.md":       "modules",
		"archive/2026-05-foo.md":    "archive",
		"local/current.main.md":     "current",
		"local/current.feature.md":  "current",
		"sessions/2026-05-26.md":    "sessions",
	}
	for path, wantName := range cases {
		t.Run(path, func(t *testing.T) {
			cat, ok := s.CategoryForPath(path)
			if !ok {
				t.Fatalf("%q not matched", path)
			}
			if cat.Name != wantName {
				t.Errorf("Name = %q, want %q", cat.Name, wantName)
			}
		})
	}
}

func TestCategoryForPath_NoMatch(t *testing.T) {
	s := DefaultSchema()
	s.populateCategoryNames()
	cases := []string{
		"random.txt",
		"modules/auth/extra/deep.md", // not matched by modules/*.md (single-segment glob)
		"unknown/file.md",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			if _, ok := s.CategoryForPath(path); ok {
				t.Errorf("%q should not match any category", path)
			}
		})
	}
}

func TestCategoryForPath_NamePopulated(t *testing.T) {
	s := DefaultSchema()
	s.populateCategoryNames()
	cat, ok := s.CategoryForPath("modules/auth.md")
	if !ok {
		t.Fatal("modules/auth.md not matched")
	}
	if cat.Name == "" {
		t.Error("Category.Name not populated after CategoryForPath")
	}
}

// ---------- Load / Write ----------

func TestLoadSchema_AppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	// Minimal YAML — only version set.
	if err := os.WriteFile(path, []byte("version: \"0.4.1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSchema(path)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	if s.Version != "0.4.1" {
		t.Errorf("Version = %q, want 0.4.1", s.Version)
	}
	// Defaults' categories must survive (yaml.v3 merges, doesn't replace
	// pre-populated maps when the YAML doesn't mention them).
	if _, ok := s.Categories["decisions"]; !ok {
		t.Error("defaults' categories missing after load of minimal YAML")
	}
}

func TestLoadSchema_PartialCategoryOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	body := `version: "0.4.1"
categories:
  decisions:
    approval: apply
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := s.Categories["decisions"]
	if dec.Approval != ApprovalApply {
		t.Errorf("decisions.Approval = %q, want apply (overridden)", dec.Approval)
	}
	// Other fields of decisions should survive from defaults.
	if dec.File != "decisions.md" {
		t.Errorf("decisions.File = %q, want decisions.md (default)", dec.File)
	}
	// Other categories should also survive.
	if s.Categories["modules"].FileGlob != "modules/*.md" {
		t.Error("modules category lost after partial override of decisions")
	}
}

func TestLoadSchema_FileNotFound(t *testing.T) {
	_, err := LoadSchema(filepath.Join(t.TempDir(), "no-such-file.yaml"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadSchema_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	if err := os.WriteFile(path, []byte("not yaml: {{{ bad"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSchema(path)
	if err == nil {
		t.Error("expected error for malformed YAML")
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	original := DefaultSchema()
	if err := WriteSchema(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != original.Version {
		t.Errorf("Version: got %q, want %q", loaded.Version, original.Version)
	}
	if len(loaded.Categories) != len(original.Categories) {
		t.Errorf("category count: got %d, want %d",
			len(loaded.Categories), len(original.Categories))
	}
	for name, orig := range original.Categories {
		got, ok := loaded.Categories[name]
		if !ok {
			t.Errorf("category %q missing after round-trip", name)
			continue
		}
		if got.File != orig.File || got.FileGlob != orig.FileGlob {
			t.Errorf("category %q: file/glob mismatch (got %q/%q, want %q/%q)",
				name, got.File, got.FileGlob, orig.File, orig.FileGlob)
		}
		if got.Approval != orig.Approval {
			t.Errorf("category %q: Approval mismatch (got %q, want %q)",
				name, got.Approval, orig.Approval)
		}
	}
}

func TestWriteDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.yaml")
	if err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the file contains the key categories.
	for _, name := range []string{"decisions", "modules", "archive"} {
		if !strings.Contains(string(b), name) {
			t.Errorf("written schema does not mention %q", name)
		}
	}
}

// ---------- Validate ----------

func TestValidate_RequiresVersion(t *testing.T) {
	s := DefaultSchema()
	s.Version = ""
	if err := s.Validate(); err == nil {
		t.Error("expected error for empty version")
	}
}

func TestValidate_RejectsBothFileAndGlob(t *testing.T) {
	s := DefaultSchema()
	bad := s.Categories["decisions"]
	bad.FileGlob = "decisions/*.md"
	s.Categories["decisions"] = bad
	if err := s.Validate(); err == nil {
		t.Error("expected error for category with both file and file_glob")
	}
}

func TestValidate_RejectsNeitherFileNorGlob(t *testing.T) {
	s := DefaultSchema()
	bad := s.Categories["decisions"]
	bad.File = ""
	bad.FileGlob = ""
	s.Categories["decisions"] = bad
	if err := s.Validate(); err == nil {
		t.Error("expected error for category with neither file nor file_glob")
	}
}

func TestValidate_RejectsInvalidApprovalMode(t *testing.T) {
	s := DefaultSchema()
	bad := s.Categories["decisions"]
	bad.Approval = "definitely-not-valid"
	s.Categories["decisions"] = bad
	if err := s.Validate(); err == nil {
		t.Error("expected error for invalid approval mode")
	}
}

func TestValidate_RejectsServerManagedAndAgentWritable(t *testing.T) {
	s := DefaultSchema()
	bad := s.Categories["index"]
	bad.AgentWritable = true // contradicts server_managed: true
	s.Categories["index"] = bad
	if err := s.Validate(); err == nil {
		t.Error("expected error for server_managed + agent_writable combo")
	}
}
