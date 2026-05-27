package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/git"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// fixture sets up a temporary .agent-memory/ with conventions, index,
// local/current.shared.md, and one decisions file with two anchored
// sections. Returns the open Index and the assembled FetchDeps.
func fixture(t *testing.T) (FetchDeps, func()) {
	t.Helper()
	root := t.TempDir()
	memDir := filepath.Join(root, ".agent-memory")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(memDir, "modules"), 0755))
	must(os.MkdirAll(filepath.Join(memDir, "local"), 0755))
	must(os.MkdirAll(filepath.Join(memDir, "meta"), 0755))

	files := map[string]string{
		"conventions.md": "# Conventions\n<!-- @id: conventions -->\n\nRun `go test ./...` before merging.\n",
		"index.md":       "# Agent Memory Index\n<!-- @generated -->\n\nLast validated: <init>\n",
		"local/current.shared.md": "## Current Active Work\n<!-- @id: active -->\n\nLanding M2 fetch pipeline.\n",
		"decisions.md": "# Decisions\n<!-- @id: decisions -->\n\n## Use Postgres\n<!-- @id: use-postgres -->\n\nChosen for transactional storage.\n\n## Refresh Token Rotation\n<!-- @id: refresh-token-rotation -->\n\nRotate refresh tokens on every successful use.\n",
		"modules/auth.md": "## Token Module\n<!-- @id: token-module -->\n\nHandles JWT issuing and rotation.\n",
	}
	for rel, body := range files {
		must(os.WriteFile(filepath.Join(memDir, rel), []byte(body), 0644))
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	must(err)
	ctx := context.Background()
	must(idx.Init(ctx))
	sch := schema.DefaultSchema()
	must(idx.RebuildAll(ctx, memDir, sch, index.RebuildOpts{}))

	deps := FetchDeps{
		Idx:       idx,
		Schema:    sch,
		Manifest:  config.DefaultManifest(),
		MemoryDir: memDir,
		Branch:    git.BranchInfo{Name: "main", IsGitRepo: true},
	}
	return deps, func() { _ = idx.Close() }
}

// --- bootstrap (empty query) ---

func TestBuildContextPack_BootstrapHasConventionsAndShared(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	resp, err := BuildContextPack(context.Background(), FetchRequest{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Context, "Run `go test") {
		t.Errorf("bootstrap missing conventions body")
	}
	if !strings.Contains(resp.Context, "Landing M2 fetch pipeline") {
		t.Errorf("bootstrap missing shared current body")
	}
	if !strings.Contains(resp.Context, "Last validated") {
		t.Errorf("bootstrap missing index summary")
	}
	if resp.ContextMetadata.BudgetUsed == 0 {
		t.Errorf("BudgetUsed = 0 with non-empty pack")
	}
	if resp.ContextMetadata.BudgetRemaining < 0 {
		t.Errorf("BudgetRemaining negative: %d", resp.ContextMetadata.BudgetRemaining)
	}
}

func TestBuildContextPack_BootstrapOmitsMissingBranchLocal(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()
	// fixture doesn't create local/current.main.md. The bootstrap should
	// silently skip it (no error, no entry in omitted).
	resp, err := BuildContextPack(context.Background(), FetchRequest{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range resp.IncludedFiles {
		if f.Path == "local/current.main.md" {
			t.Errorf("missing branch-local should not be in IncludedFiles")
		}
	}
}

func TestBuildContextPack_BootstrapBudgetEnforced(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	// Tight budget — only fits one or two files.
	resp, err := BuildContextPack(context.Background(), FetchRequest{Budget: 80}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContextMetadata.BudgetUsed > 80 {
		t.Errorf("BudgetUsed = %d exceeds requested 80", resp.ContextMetadata.BudgetUsed)
	}
	if len(resp.Omitted) == 0 {
		t.Errorf("expected some omitted files with tight budget")
	}
}

// --- search (non-empty query) ---

func TestBuildContextPack_SearchReturnsRelevantSection(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	resp, err := BuildContextPack(context.Background(), FetchRequest{Query: "refresh token rotation"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Context, "Refresh Token Rotation") {
		t.Errorf("search pack missing refresh-token section\ncontext:\n%s", resp.Context)
	}
	// The matched file appears in IncludedFiles with section count >= 1.
	found := false
	for _, f := range resp.IncludedFiles {
		if f.Path == "decisions.md" && f.SectionCount >= 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("decisions.md not in IncludedFiles: %+v", resp.IncludedFiles)
	}
}

func TestBuildContextPack_SearchAlwaysPrependsCurrentShared(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	resp, err := BuildContextPack(context.Background(), FetchRequest{Query: "postgres"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	// The shared-current header should come BEFORE the matched section.
	sharedIdx := strings.Index(resp.Context, "@file: local/current.shared.md")
	postgresIdx := strings.Index(resp.Context, "Use Postgres")
	if sharedIdx < 0 || postgresIdx < 0 {
		t.Fatalf("missing markers: shared=%d postgres=%d\n%s", sharedIdx, postgresIdx, resp.Context)
	}
	if sharedIdx > postgresIdx {
		t.Errorf("shared current should precede search results")
	}
}

func TestBuildContextPack_ExcludeArchive(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	// Drop an archive section that mentions the query term strongly.
	archiveDir := filepath.Join(deps.MemoryDir, "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "2026-05-foo.md"),
		[]byte("## Old Refresh Token Notes\n<!-- @id: old-refresh -->\n\nRefresh token historical notes.\n"),
		0644); err != nil {
		t.Fatal(err)
	}
	// Re-index so the archive file is searchable.
	ctx := context.Background()
	if err := deps.Idx.RebuildAll(ctx, deps.MemoryDir, deps.Schema, index.RebuildOpts{}); err != nil {
		t.Fatal(err)
	}

	resp, err := BuildContextPack(ctx, FetchRequest{Query: "refresh token", ExcludeArchive: true}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(resp.Context, "@file: archive/") {
		t.Errorf("archive file leaked into pack despite ExcludeArchive\n%s", resp.Context)
	}
	// The non-archive refresh-token section must still be there.
	if !strings.Contains(resp.Context, "Refresh Token Rotation") {
		t.Errorf("non-archive refresh section missing")
	}
}

func TestBuildContextPack_ScopeBoost(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	// Search for a term that matches both modules/auth.md ("token module")
	// and decisions.md ("refresh token rotation"). Without scope, both
	// match. With scope=["modules"], modules/auth should rank first.
	resp, err := BuildContextPack(context.Background(), FetchRequest{
		Query: "token",
		Scope: []string{"modules"},
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	// modules/auth.md section should appear BEFORE decisions.md content.
	authIdx := strings.Index(resp.Context, "@file: modules/auth.md")
	decisionsIdx := strings.Index(resp.Context, "@file: decisions.md")
	if authIdx < 0 {
		t.Fatalf("modules/auth section not in pack\n%s", resp.Context)
	}
	if decisionsIdx > 0 && authIdx > decisionsIdx {
		t.Errorf("scope boost didn't move modules above decisions:\n  auth at %d, decisions at %d",
			authIdx, decisionsIdx)
	}
}

func TestBuildContextPack_RecordsActiveBranch(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	resp, err := BuildContextPack(context.Background(), FetchRequest{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContextMetadata.ActiveBranch != "main" {
		t.Errorf("ActiveBranch = %q, want main", resp.ContextMetadata.ActiveBranch)
	}
}

func TestBuildContextPack_NoGitRepoFallsBackToShared(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()
	deps.Branch = git.BranchInfo{IsGitRepo: false}

	resp, err := BuildContextPack(context.Background(), FetchRequest{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	// shared file IS in the bootstrap regardless of git state.
	if !strings.Contains(resp.Context, "local/current.shared.md") {
		t.Errorf("shared current missing when no git repo")
	}
	if resp.ContextMetadata.ActiveBranch != "" {
		t.Errorf("ActiveBranch should be empty without git, got %q", resp.ContextMetadata.ActiveBranch)
	}
}
