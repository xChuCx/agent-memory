package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/cli"
)

// mcpFixture inits an .agent-memory/ in a temp dir (via the same init we
// ship in the CLI) and seeds one extra anchored section so the search
// path has something to find.
func mcpFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// We borrow cli's init logic — it's the canonical bootstrap and we
	// want to stay in lockstep with what `agent-memory init` produces.
	root := cli.NewRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"init", "--root", dir, "--name", "mcp-test"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, ".agent-memory", "modules", "auth.md"),
		[]byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate on every successful use.\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunFetchContext_EmptyQueryBootstrap(t *testing.T) {
	dir := mcpFixture(t)
	out, err := runFetchContext(context.Background(), dir, FetchContextInput{})
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
	out, err := runFetchContext(context.Background(), dir, FetchContextInput{
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
	out, err := runFetchContext(context.Background(), dir, FetchContextInput{
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
	_, err := runFetchContext(context.Background(), dir, FetchContextInput{})
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
