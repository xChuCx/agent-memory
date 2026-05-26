package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/schema"
)

// ---------- DefaultManifest ----------

func TestDefaultManifest_PassesValidate(t *testing.T) {
	m := DefaultManifest()
	if err := m.Validate(); err != nil {
		t.Errorf("default manifest failed Validate: %v", err)
	}
}

func TestDefaultManifest_ApprovalShape(t *testing.T) {
	m := DefaultManifest()
	cases := map[string]struct {
		got, want schema.ApprovalMode
	}{
		"decisions":        {m.Updates.Approval.Decisions, schema.ApprovalStage},
		"conventions":      {m.Updates.Approval.Conventions, schema.ApprovalStage},
		"modules":          {m.Updates.Approval.Modules, schema.ApprovalStage},
		"pitfalls_replace": {m.Updates.Approval.PitfallsReplace, schema.ApprovalStage},
		"pitfalls_append":  {m.Updates.Approval.PitfallsAppend, schema.ApprovalApply},
		"archive":          {m.Updates.Approval.Archive, schema.ApprovalStage},
		"current":          {m.Updates.Approval.Current, schema.ApprovalApply},
		"current_shared":   {m.Updates.Approval.CurrentShared, schema.ApprovalApply},
		"sessions":         {m.Updates.Approval.Sessions, schema.ApprovalApply},
		"index":            {m.Updates.Approval.Index, schema.ApprovalServerOnly},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s: got %q, want %q", name, c.got, c.want)
			}
		})
	}
}

func TestDefaultManifest_Budgets(t *testing.T) {
	m := DefaultManifest()
	if m.Budgets.BootstrapChars != 12000 {
		t.Errorf("BootstrapChars = %d, want 12000", m.Budgets.BootstrapChars)
	}
	if m.Budgets.FetchContextChars != 24000 {
		t.Errorf("FetchContextChars = %d, want 24000", m.Budgets.FetchContextChars)
	}
	if m.Staging.TTLSeconds != 604800 {
		t.Errorf("TTLSeconds = %d, want 604800", m.Staging.TTLSeconds)
	}
}

// ---------- Load ----------

func TestLoadManifest_DefaultsAppliedToMinimal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte("version: \"0.4.1\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Budgets.BootstrapChars != 12000 {
		t.Errorf("BootstrapChars not defaulted: got %d", m.Budgets.BootstrapChars)
	}
	if m.Updates.Approval.Decisions != schema.ApprovalStage {
		t.Errorf("Decisions not defaulted: got %q", m.Updates.Approval.Decisions)
	}
}

func TestLoadManifest_PartialApprovalOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	body := `version: "0.4.1"
updates:
  approval:
    current: stage
    sessions: stage
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Updates.Approval.Current != schema.ApprovalStage {
		t.Errorf("Current = %q, want stage (overridden)", m.Updates.Approval.Current)
	}
	if m.Updates.Approval.Sessions != schema.ApprovalStage {
		t.Errorf("Sessions = %q, want stage (overridden)", m.Updates.Approval.Sessions)
	}
	// Untouched fields preserve defaults.
	if m.Updates.Approval.Decisions != schema.ApprovalStage {
		t.Errorf("Decisions = %q, want stage (default)", m.Updates.Approval.Decisions)
	}
	if m.Updates.Approval.PitfallsAppend != schema.ApprovalApply {
		t.Errorf("PitfallsAppend = %q, want apply (default)", m.Updates.Approval.PitfallsAppend)
	}
}

func TestLoadManifest_FileNotFound(t *testing.T) {
	_, err := LoadManifest(filepath.Join(t.TempDir(), "no-such.yaml"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadManifest_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte("not yaml: {{{ bad"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(path); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

func TestLoadManifest_LegacyLockTTLAcceptedAndIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	body := `version: "0.4.1"
concurrency:
  lock_ttl_seconds: 30
  wait_timeout_seconds: 15
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	// Field is parsed (no error), but the lock layer ignores it.
	if m.Concurrency.LockTTLSeconds != 30 {
		t.Errorf("LockTTLSeconds: got %d, want 30 (round-tripped)", m.Concurrency.LockTTLSeconds)
	}
	if m.Concurrency.WaitTimeoutSeconds != 15 {
		t.Errorf("WaitTimeoutSeconds: got %d, want 15", m.Concurrency.WaitTimeoutSeconds)
	}
}

// ---------- Write / RoundTrip ----------

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	original := DefaultManifest()
	original.Project.Name = "round-trip-test"
	original.Budgets.MaxFileChars = 50000

	if err := WriteManifest(path, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Project.Name != "round-trip-test" {
		t.Errorf("Project.Name: got %q", loaded.Project.Name)
	}
	if loaded.Budgets.MaxFileChars != 50000 {
		t.Errorf("MaxFileChars: got %d, want 50000", loaded.Budgets.MaxFileChars)
	}
	if loaded.Updates.Approval.Decisions != original.Updates.Approval.Decisions {
		t.Error("Approval round-trip failed")
	}
}

func TestWriteDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := WriteDefault(path, "my-project"); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Project.Name != "my-project" {
		t.Errorf("Project.Name = %q, want my-project", m.Project.Name)
	}
	// File should not contain "lock_ttl_seconds" (it's omitempty in fresh manifests).
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "lock_ttl_seconds") {
		t.Errorf("fresh manifest contains lock_ttl_seconds; should be omitempty")
	}
}

// ---------- Validate ----------

func TestValidate_RequiresVersion(t *testing.T) {
	m := DefaultManifest()
	m.Version = ""
	if err := m.Validate(); err == nil {
		t.Error("expected error for empty version")
	}
}

func TestValidate_RejectsInvalidApprovalMode(t *testing.T) {
	m := DefaultManifest()
	m.Updates.Approval.Decisions = "garbage-mode"
	if err := m.Validate(); err == nil {
		t.Error("expected error for invalid approval mode")
	}
}

func TestValidate_RejectsZeroBudget(t *testing.T) {
	cases := []func(*Manifest){
		func(m *Manifest) { m.Budgets.BootstrapChars = 0 },
		func(m *Manifest) { m.Budgets.FetchContextChars = 0 },
		func(m *Manifest) { m.Budgets.MaxFileChars = 0 },
		func(m *Manifest) { m.Staging.TTLSeconds = 0 },
	}
	for i, mutate := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			m := DefaultManifest()
			mutate(m)
			if err := m.Validate(); err == nil {
				t.Error("expected error for zero budget/TTL")
			}
		})
	}
}

func TestValidate_AcceptsZeroWaitTimeout(t *testing.T) {
	// WaitTimeout=0 means "TryLock once, fail fast" — legitimate config.
	m := DefaultManifest()
	m.Concurrency.WaitTimeoutSeconds = 0
	if err := m.Validate(); err != nil {
		t.Errorf("WaitTimeoutSeconds=0 should be accepted: %v", err)
	}
}
