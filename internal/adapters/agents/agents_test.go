package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsContent_PlainMarkdown(t *testing.T) {
	body := string(AgentsContent())
	// AGENTS.md is plain markdown — no YAML frontmatter (unlike .mdc).
	if strings.HasPrefix(body, "---\n") {
		t.Errorf("AGENTS.md should NOT have YAML frontmatter; got:\n%s", body[:80])
	}
	if !strings.HasPrefix(body, "# ") {
		t.Errorf("AGENTS.md should start with a top-level heading; got:\n%s", body[:80])
	}
}

func TestAgentsContent_MentionsBothTools(t *testing.T) {
	body := string(AgentsContent())
	for _, want := range []string{"memory.fetch_context", "memory.propose_update"} {
		if !strings.Contains(body, want) {
			t.Errorf("AGENTS.md missing %q", want)
		}
	}
}

func TestAgentsContent_DocumentsRejectReasons(t *testing.T) {
	body := string(AgentsContent())
	for _, code := range []string{"secret_detected", "provenance_violation", "target_drift"} {
		if !strings.Contains(body, code) {
			t.Errorf("AGENTS.md missing reject reason %q", code)
		}
	}
}

func TestInstall_ProjectLocal(t *testing.T) {
	root := t.TempDir()
	res, err := Install(Options{Root: root})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := filepath.Join(root, "AGENTS.md")
	if len(res.Files) != 1 || res.Files[0] != want {
		t.Errorf("Files = %v, want [%s]", res.Files, want)
	}
	body, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "agent-memory") {
		t.Error("installed AGENTS.md doesn't match embedded asset")
	}
}

func TestInstall_RejectsUserGlobal(t *testing.T) {
	_, err := Install(Options{Root: t.TempDir(), UserGlobal: true})
	if err == nil {
		t.Fatal("expected error when UserGlobal: true")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("err = %q, want mention of 'not supported'", err)
	}
}

func TestInstall_RequiresRoot(t *testing.T) {
	_, err := Install(Options{})
	if err == nil {
		t.Fatal("expected error when Root is empty")
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
		t.Errorf("not idempotent: %+v", res)
	}
}

func TestInstall_ForceOverwrites(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "AGENTS.md")
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
