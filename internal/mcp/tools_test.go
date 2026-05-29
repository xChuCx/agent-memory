package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// mcpFixture scaffolds an .agent-memory/ in a temp dir directly via the
// config/schema primitives, NOT via cli's init command.
//
// Why not call cli.NewRootCmd? internal/cli imports internal/mcp (for the
// `mcp` subcommand), so importing cli from here forms a test-time import
// cycle:
//
//   mcp/tools_test.go → cli → mcp
//
// Building the same layout directly through the same primitives keeps
// the test independent and stays in lockstep with `agent-memory init`
// (both paths use config.WriteDefault and schema.WriteDefault).
func mcpFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".agent-memory")

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"modules", "local", "meta"} {
		must(os.MkdirAll(filepath.Join(memDir, d), 0755))
	}
	must(schema.WriteDefault(filepath.Join(memDir, "meta", "schema.yaml")))
	must(config.WriteDefault(filepath.Join(memDir, "meta", "manifest.yaml"), "mcp-test"))

	files := map[string]string{
		"conventions.md": "# Conventions\n<!-- @id: conventions -->\n\nRun `go test ./...` before merging.\n",
		"index.md":       "# Agent Memory Index\n<!-- @generated -->\n\nLast validated: <init>\n",
		"decisions.md":   "# Decisions\n<!-- @id: decisions -->\n\nProject decisions land here.\n",
		"pitfalls.md":    "# Pitfalls\n<!-- @id: pitfalls -->\n\nKnown traps.\n",
		filepath.Join("modules", "auth.md"): "## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate on every successful use.\n",
	}
	for rel, body := range files {
		must(os.WriteFile(filepath.Join(memDir, rel), []byte(body), 0644))
	}
	return dir
}

func TestRunFetchContext_EmptyQueryBootstrap(t *testing.T) {
	dir := mcpFixture(t)
	out, err := runFetchContext(context.Background(), dir, nil, FetchContextInput{})
	if err != nil {
		t.Fatalf("runFetchContext: %v", err)
	}
	if !strings.Contains(out.Context, "Conventions") {
		t.Errorf("bootstrap missing conventions section\n%s", out.Context)
	}
	if out.ContextMetadata.BudgetUsed == 0 {
		t.Errorf("BudgetUsed = 0 with non-empty pack")
	}
}

func TestRunFetchContext_QueryMatchesIndexedSection(t *testing.T) {
	dir := mcpFixture(t)
	out, err := runFetchContext(context.Background(), dir, nil, FetchContextInput{
		Query: "refresh token",
	})
	if err != nil {
		t.Fatalf("runFetchContext: %v", err)
	}
	if !strings.Contains(out.Context, "Token Rotation") {
		t.Errorf("expected token-rotation section in pack:\n%s", out.Context)
	}
}

func TestRunFetchContext_ScopeBoost(t *testing.T) {
	dir := mcpFixture(t)
	out, err := runFetchContext(context.Background(), dir, nil, FetchContextInput{
		Query: "token",
		Scope: []string{"modules"},
	})
	if err != nil {
		t.Fatalf("runFetchContext: %v", err)
	}
	if !strings.Contains(out.Context, "@file: modules/auth.md") {
		t.Errorf("modules/auth.md not in pack with scope=modules\n%s", out.Context)
	}
}

func TestRunFetchContext_RejectsMissingAgentMemory(t *testing.T) {
	dir := t.TempDir() // no init
	_, err := runFetchContext(context.Background(), dir, nil, FetchContextInput{})
	if err == nil {
		t.Fatal("expected error for missing .agent-memory/")
	}
}

func TestNewServerRegisterTools(t *testing.T) {
	// Smoke test: New + RegisterTools must succeed even with a non-existent
	// root. The registration doesn't open the index yet — that happens on
	// the first tool call.
	dir := t.TempDir()
	srv := New(dir, "test-0.0.0")
	if err := srv.RegisterTools(); err != nil {
		t.Errorf("RegisterTools: %v", err)
	}
}
