package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contains(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// =============================================================================
// SkillContent: embedded asset sanity checks
// =============================================================================

func TestSkillContent_HasFrontmatter(t *testing.T) {
	body := string(SkillContent())
	if !strings.HasPrefix(body, "---\n") {
		t.Fatal("SKILL.md missing leading frontmatter delimiter")
	}
	if !strings.Contains(body, "name: agent-memory") {
		t.Error("SKILL.md frontmatter missing name: agent-memory")
	}
	if !strings.Contains(body, "description:") {
		t.Error("SKILL.md frontmatter missing description")
	}
}

func TestSkillContent_MentionsBothTools(t *testing.T) {
	body := string(SkillContent())
	for _, want := range []string{"memory.fetch_context", "memory.propose_update"} {
		if !strings.Contains(body, want) {
			t.Errorf("SKILL.md missing reference to %q", want)
		}
	}
}

func TestSkillContent_DocumentsRejectReasons(t *testing.T) {
	// Regression sentinel: the reject-reason table is the agent's main
	// debugging tool when its proposals fail. Spot-check a handful of
	// the wire-stable codes.
	body := string(SkillContent())
	codes := []string{
		"secret_detected",
		"provenance_violation",
		"target_drift",
		"invalid_intent",
	}
	for _, code := range codes {
		if !strings.Contains(body, code) {
			t.Errorf("SKILL.md doesn't mention reject reason %q", code)
		}
	}
}

func TestSkillContent_TeachesBootstrapCall(t *testing.T) {
	// The bootstrap fetch_context (empty query) is the single most
	// important behavior to teach. Verify the explicit empty-args
	// example is there.
	body := string(SkillContent())
	if !strings.Contains(body, `"arguments": {}`) {
		t.Error("SKILL.md missing explicit empty-args fetch_context example")
	}
}

// =============================================================================
// Install: project-local
// =============================================================================

func TestInstall_ProjectLocal_WritesSkillAndMCPConfig(t *testing.T) {
	root := t.TempDir()
	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	skill := filepath.Join(root, ".claude", "skills", "agent-memory", "SKILL.md")
	mcp := filepath.Join(root, ".mcp.json")
	if !contains(res.Files, skill) {
		t.Errorf("Files %v missing skill %s", res.Files, skill)
	}
	if !contains(res.Files, mcp) {
		t.Errorf("Files %v missing .mcp.json %s", res.Files, mcp)
	}

	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("read installed skill: %v", err)
	}
	if !strings.Contains(string(body), "agent-memory") {
		t.Error("installed SKILL.md doesn't match embedded asset")
	}

	// The MCP registration must be project-portable: the server resolves the
	// repo from $CLAUDE_PROJECT_DIR at spawn, not a hardcoded path.
	mb, err := os.ReadFile(mcp)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	for _, want := range []string{`"agent-memory"`, `"mcp"`, `${CLAUDE_PROJECT_DIR:-.}`} {
		if !strings.Contains(string(mb), want) {
			t.Errorf(".mcp.json missing %q:\n%s", want, mb)
		}
	}
}

func TestInstall_MergesExistingMCPConfig(t *testing.T) {
	root := t.TempDir()
	// A pre-existing .mcp.json with another server + an unrelated top-level key.
	pre := `{"mcpServers":{"other":{"command":"x"}},"foo":"bar"}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(pre), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Options{Root: root}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("merged .mcp.json is invalid JSON: %v\n%s", err, raw)
	}
	if doc["foo"] != "bar" {
		t.Errorf("merge dropped the unrelated top-level key: %v", doc)
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers["other"] == nil {
		t.Errorf("merge clobbered the existing 'other' server: %v", servers)
	}
	if servers[MCPServerName] == nil {
		t.Errorf("merge didn't register %q: %v", MCPServerName, servers)
	}
}

func TestInstall_NoForce_RefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "agent-memory")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := []byte("user customisation goes here\n")
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, existing, 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	// The skill must be refused (preserved), regardless of the .mcp.json write.
	if contains(res.Files, skillPath) {
		t.Errorf("skill should not be overwritten without --force; Files=%v", res.Files)
	}
	if !contains(res.Skipped, skillPath) {
		t.Errorf("skill should be reported skipped; Skipped=%v", res.Skipped)
	}
	// Existing content must be preserved byte-for-byte.
	got, _ := os.ReadFile(skillPath)
	if string(got) != string(existing) {
		t.Errorf("existing SKILL.md was modified: got %q, want %q", got, existing)
	}
}

func TestInstall_Force_Overwrites(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "agent-memory")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("stale\n"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Install(Options{Root: root, Force: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !contains(res.Files, skillPath) {
		t.Fatalf("force install should rewrite the skill; Files=%v", res.Files)
	}
	got, _ := os.ReadFile(skillPath)
	if !strings.Contains(string(got), "agent-memory") {
		t.Error("force install didn't replace stale content")
	}
}

// =============================================================================
// Install: user-global
// =============================================================================

func TestInstall_UserGlobal_UsesHomeOverride(t *testing.T) {
	fakeHome := t.TempDir()
	res, err := Install(Options{UserGlobal: true, HomeDir: fakeHome})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := filepath.Join(fakeHome, ".claude", "skills", "agent-memory", "SKILL.md")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("user-global skill not at %s: %v", want, err)
	}
}

func TestInstall_RequiresRootOrUserGlobal(t *testing.T) {
	// No Root, no UserGlobal → configuration error.
	_, err := Install(Options{})
	if err == nil {
		t.Fatal("expected error when neither Root nor UserGlobal is set")
	}
	if !strings.Contains(err.Error(), "Root is required") {
		t.Errorf("err = %q, want mention of Root requirement", err)
	}
}

// =============================================================================
// Re-install idempotency: second install without --force is a no-op
// =============================================================================

func TestInstall_IdempotentWithoutForce(t *testing.T) {
	root := t.TempDir()
	if _, err := Install(Options{Root: root}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(res.Files) != 0 {
		t.Errorf("second install wrote files: %v (should have skipped)", res.Files)
	}
	// Both artifacts (skill + .mcp.json) are now present, so both are skipped.
	if len(res.Skipped) != 2 {
		t.Errorf("Skipped = %v, want two entries (skill + .mcp.json)", res.Skipped)
	}
}
