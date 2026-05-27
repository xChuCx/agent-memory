package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// RuleContent: embedded asset sanity
// =============================================================================

func TestRuleContent_HasFrontmatter(t *testing.T) {
	body := string(RuleContent())
	if !strings.HasPrefix(body, "---\n") {
		t.Fatal(".mdc missing leading YAML frontmatter")
	}
	if !strings.Contains(body, "description:") {
		t.Error(".mdc frontmatter missing description")
	}
	if !strings.Contains(body, "alwaysApply:") {
		t.Error(".mdc frontmatter missing alwaysApply")
	}
}

func TestRuleContent_MentionsBothTools(t *testing.T) {
	body := string(RuleContent())
	for _, want := range []string{"memory.fetch_context", "memory.propose_update"} {
		if !strings.Contains(body, want) {
			t.Errorf(".mdc missing reference to %q", want)
		}
	}
}

func TestRuleContent_DocumentsRejectReasons(t *testing.T) {
	body := string(RuleContent())
	for _, code := range []string{"secret_detected", "provenance_violation", "target_drift"} {
		if !strings.Contains(body, code) {
			t.Errorf(".mdc missing reject reason %q", code)
		}
	}
}

// =============================================================================
// Install: project-local
// =============================================================================

func TestInstall_ProjectLocal(t *testing.T) {
	root := t.TempDir()
	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := filepath.Join(root, ".cursor", "rules", "agent-memory.mdc")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "agent-memory") {
		t.Error("installed .mdc doesn't match embedded asset")
	}
}

func TestInstall_NoForceRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".cursor", "rules", "agent-memory.mdc")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	existing := []byte("custom user content\n")
	if err := os.WriteFile(target, existing, 0644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 0 {
		t.Errorf("Files = %v, want empty", res.Files)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != target {
		t.Errorf("Skipped = %v, want [%s]", res.Skipped, target)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(existing) {
		t.Errorf("existing rule was modified: got %q, want %q", got, existing)
	}
}

func TestInstall_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".cursor", "rules", "agent-memory.mdc")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := Install(Options{Root: root, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Errorf("Files = %v, want one", res.Files)
	}
	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "memory.fetch_context") {
		t.Error("force install didn't replace stale content")
	}
}

// =============================================================================
// Install: user-global (Cursor supports it)
// =============================================================================

func TestInstall_UserGlobalUsesHomeOverride(t *testing.T) {
	fakeHome := t.TempDir()
	res, err := Install(Options{UserGlobal: true, HomeDir: fakeHome})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(fakeHome, ".cursor", "rules", "agent-memory.mdc")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
}

func TestInstall_RequiresRootOrUserGlobal(t *testing.T) {
	_, err := Install(Options{})
	if err == nil {
		t.Fatal("expected error when neither Root nor UserGlobal set")
	}
}

func TestInstall_IdempotentWithoutForce(t *testing.T) {
	root := t.TempDir()
	if _, err := Install(Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 0 || len(res.Skipped) != 1 {
		t.Errorf("second install not idempotent: %+v", res)
	}
}
