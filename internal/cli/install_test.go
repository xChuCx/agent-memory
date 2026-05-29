package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// =============================================================================
// runInstall: claude adapter
// =============================================================================

func TestRunInstall_ClaudeProjectLocal(t *testing.T) {
	root := t.TempDir()
	res, err := runInstall(installOptions{
		Adapter: "claude",
		Root:    root,
	})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if res.Adapter != "claude" {
		t.Errorf("Adapter = %q, want claude", res.Adapter)
	}
	wantPath := filepath.Join(root, ".claude", "skills", "agent-memory", "SKILL.md")
	if len(res.Files) != 1 || res.Files[0] != wantPath {
		t.Errorf("Files = %v, want [%s]", res.Files, wantPath)
	}
	body, _ := os.ReadFile(wantPath)
	if !strings.Contains(string(body), "memory.fetch_context") {
		t.Error("installed SKILL.md missing tool reference")
	}
}

func TestRunInstall_UnknownAdapter(t *testing.T) {
	_, err := runInstall(installOptions{
		Adapter: "not-a-real-adapter",
		Root:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for unknown adapter")
	}
	if !strings.Contains(err.Error(), "unknown adapter") {
		t.Errorf("err = %q, want mention of 'unknown adapter'", err)
	}
}

func TestRunInstall_RefusesOverwriteWithoutForce(t *testing.T) {
	root := t.TempDir()
	// First install: populates the skill.
	if _, err := runInstall(installOptions{Adapter: "claude", Root: root}); err != nil {
		t.Fatal(err)
	}
	// Second install (no force): nothing written, file is skipped.
	res, err := runInstall(installOptions{Adapter: "claude", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 0 {
		t.Errorf("Files = %v, want empty (no overwrite)", res.Files)
	}
	if len(res.Skipped) != 1 {
		t.Errorf("Skipped = %v, want one entry", res.Skipped)
	}
}

func TestRunInstall_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".claude", "skills", "agent-memory")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := runInstall(installOptions{
		Adapter: "claude",
		Root:    root,
		Force:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 1 {
		t.Errorf("Files = %v, want [installed path]", res.Files)
	}
	got, _ := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if !strings.Contains(string(got), "memory.propose_update") {
		t.Error("force install didn't replace stale content")
	}
}

// =============================================================================
// Cobra integration: end-to-end through NewRootCmd
// =============================================================================

func TestCobra_InstallClaude_HumanOutput(t *testing.T) {
	root := t.TempDir()
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"install", "claude", "--root", root})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install claude: %v (stderr=%q)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Installed claude adapter") {
		t.Errorf("stdout missing install banner: %q", stdout.String())
	}
	// Skill file is actually on disk.
	wantPath := filepath.Join(root, ".claude", "skills", "agent-memory", "SKILL.md")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("skill file not at %s: %v", wantPath, err)
	}
}

func TestCobra_InstallClaude_JSON(t *testing.T) {
	root := t.TempDir()
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"install", "claude", "--root", root, "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("install claude --json: %v (stderr=%q)", err, stderr.String())
	}
	var got InstallResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout.String())
	}
	if got.Adapter != "claude" {
		t.Errorf("Adapter = %q, want claude", got.Adapter)
	}
	if len(got.Files) != 1 {
		t.Errorf("Files = %v, want one entry", got.Files)
	}
}

func TestCobra_InstallClaude_NoForceShowsPreserved(t *testing.T) {
	root := t.TempDir()
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)

	// First install: writes the skill.
	cmd.SetArgs([]string{"install", "claude", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()

	// Second install (no --force): "already installed" banner + preserved.
	cmd2 := NewRootCmd()
	cmd2.SetOut(&stdout)
	cmd2.SetErr(&stdout)
	cmd2.SetArgs([]string{"install", "claude", "--root", root})
	if err := cmd2.Execute(); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "already installed") {
		t.Errorf("missing 'already installed' banner: %q", out)
	}
	if !strings.Contains(out, "--force") {
		t.Errorf("missing hint about --force: %q", out)
	}
}

func TestCobra_InstallUnknownAdapter_ReturnsError(t *testing.T) {
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"install", "not-real", "--root", t.TempDir()})

	if err := cmd.Execute(); err == nil {
		t.Error("expected non-zero exit for unknown adapter")
	}
}

// =============================================================================
// Multi-adapter coverage: every supported adapter installs cleanly
// through the CLI dispatch.
// =============================================================================

func TestRunInstall_CursorProjectLocal(t *testing.T) {
	root := t.TempDir()
	res, err := runInstall(installOptions{Adapter: "cursor", Root: root})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if res.Adapter != "cursor" {
		t.Errorf("Adapter = %q, want cursor", res.Adapter)
	}
	want := filepath.Join(root, ".cursor", "rules", "agent-memory.mdc")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
}

func TestRunInstall_AgentsProjectLocal(t *testing.T) {
	root := t.TempDir()
	res, err := runInstall(installOptions{Adapter: "agents", Root: root})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	want := filepath.Join(root, "AGENTS.md")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
}

func TestRunInstall_GeminiProjectLocal(t *testing.T) {
	root := t.TempDir()
	res, err := runInstall(installOptions{Adapter: "gemini", Root: root})
	if err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	want := filepath.Join(root, "GEMINI.md")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
}

// TestRunInstall_RelativeRootResolvedToAbsolute is a regression test for a
// bug found while dogfooding on another agent:
//
//	install gemini: gemini install: write GEMINI.md:
//	WriteAtomic: path must be absolute: "GEMINI.md"
//
// A relative --root (e.g. ".") was passed straight through to the adapter,
// which joined it with the adapter filename and handed WriteAtomic a
// relative path. runInstall must resolve --root to an absolute path for
// every project-local adapter before dispatch.
func TestRunInstall_RelativeRootResolvedToAbsolute(t *testing.T) {
	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Restore cwd BEFORE t.TempDir's own cleanup removes the dir (LIFO:
	// this runs first), so Windows can delete a dir that isn't the cwd.
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	res, err := runInstall(installOptions{Adapter: "gemini", Root: "."})
	if err != nil {
		t.Fatalf("runInstall with relative root: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("Files = %v, want exactly one", res.Files)
	}
	if !filepath.IsAbs(res.Files[0]) {
		t.Errorf("installed path is not absolute: %q", res.Files[0])
	}
	if _, err := os.Stat(res.Files[0]); err != nil {
		t.Errorf("GEMINI.md not written at %q: %v", res.Files[0], err)
	}
}

func TestRunInstall_AgentsRejectsUserGlobal(t *testing.T) {
	_, err := runInstall(installOptions{Adapter: "agents", UserGlobal: true})
	if err == nil {
		t.Fatal("expected error: agents adapter does not support --user-global")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %q, want mention of 'not supported'", err)
	}
}

func TestRunInstall_GeminiRejectsUserGlobal(t *testing.T) {
	_, err := runInstall(installOptions{Adapter: "gemini", UserGlobal: true})
	if err == nil {
		t.Fatal("expected error: gemini adapter does not support --user-global")
	}
}

func TestRunInstall_AllAdaptersInSupportedList(t *testing.T) {
	for _, want := range []string{"claude", "cursor", "agents", "gemini"} {
		found := false
		for _, got := range supportedAdapters {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("supportedAdapters missing %q (full list: %v)", want, supportedAdapters)
		}
	}
}

func TestCobra_InstallCursor(t *testing.T) {
	root := t.TempDir()
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", "cursor", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install cursor: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, ".cursor", "rules", "agent-memory.mdc")); err != nil {
		t.Errorf("cursor rule not at expected path: %v", err)
	}
}

func TestCobra_InstallAgents(t *testing.T) {
	root := t.TempDir()
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", "agents", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install agents: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md not at root: %v", err)
	}
}

func TestCobra_InstallGemini(t *testing.T) {
	root := t.TempDir()
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"install", "gemini", "--root", root})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("install gemini: %v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(filepath.Join(root, "GEMINI.md")); err != nil {
		t.Errorf("GEMINI.md not at root: %v", err)
	}
}
