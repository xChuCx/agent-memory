package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/memory"
)

// fetchFixture inits an .agent-memory/ in a temp dir and drops a couple of
// content files into it so search has something to find. Returns the
// repo root path.
func fetchFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "fetch-test"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	// Add a module file with an anchored section.
	modulesDir := filepath.Join(dir, ".agent-memory", "modules")
	if err := os.WriteFile(filepath.Join(modulesDir, "auth.md"),
		[]byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate on every successful use.\n"),
		0644); err != nil {
		t.Fatal(err)
	}
	// Append a decision to decisions.md (already contains a top-level
	// "Decisions" section from init's template).
	decisionsPath := filepath.Join(dir, ".agent-memory", "decisions.md")
	dec, _ := os.ReadFile(decisionsPath)
	addendum := "\n## Use Postgres\n<!-- @id: use-postgres -->\n\nChosen for transactional storage.\n"
	if err := os.WriteFile(decisionsPath, append(dec, []byte(addendum)...), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFetch_EmptyQueryReturnsBootstrap(t *testing.T) {
	dir := fetchFixture(t)
	resp, err := runFetch(context.Background(), fetchOptions{Root: dir})
	if err != nil {
		t.Fatalf("runFetch: %v", err)
	}
	if !strings.Contains(resp.Context, "Conventions") {
		t.Errorf("bootstrap missing conventions section")
	}
	if !strings.Contains(resp.Context, "Agent Memory Index") {
		t.Errorf("bootstrap missing index summary")
	}
}

func TestFetch_QueryReturnsRankedResults(t *testing.T) {
	dir := fetchFixture(t)
	resp, err := runFetch(context.Background(), fetchOptions{
		Root:  dir,
		Query: "refresh token rotation",
	})
	if err != nil {
		t.Fatalf("runFetch: %v", err)
	}
	if !strings.Contains(resp.Context, "Token Rotation") {
		t.Errorf("expected modules/auth.md Token Rotation section, got:\n%s", resp.Context)
	}
	// modules/auth.md should appear in IncludedFiles.
	found := false
	for _, f := range resp.IncludedFiles {
		if f.Path == "modules/auth.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("modules/auth.md missing from IncludedFiles: %+v", resp.IncludedFiles)
	}
}

func TestFetch_RejectsMissingAgentMemory(t *testing.T) {
	dir := t.TempDir()
	_, err := runFetch(context.Background(), fetchOptions{Root: dir})
	if err == nil {
		t.Fatal("expected error for missing .agent-memory/")
	}
	if !strings.Contains(err.Error(), ".agent-memory") {
		t.Errorf("error doesn't mention .agent-memory: %v", err)
	}
}

func TestFetch_JSONOutputIsValid(t *testing.T) {
	dir := fetchFixture(t)

	out := &bytes.Buffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"fetch", "--root", dir, "--json", "refresh token"})
	if err := root.Execute(); err != nil {
		t.Fatalf("fetch --json: %v\noutput: %s", err, out.String())
	}

	var resp memory.FetchResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, out.String())
	}
	if resp.Context == "" {
		t.Error("Context is empty in JSON output")
	}
	if resp.ContextMetadata.BudgetUsed <= 0 {
		t.Error("BudgetUsed not populated")
	}
}

func TestFetch_BudgetCapsOutput(t *testing.T) {
	dir := fetchFixture(t)
	resp, err := runFetch(context.Background(), fetchOptions{
		Root:   dir,
		Budget: 100, // tight
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContextMetadata.BudgetUsed > 100 {
		t.Errorf("BudgetUsed = %d > 100", resp.ContextMetadata.BudgetUsed)
	}
}

func TestFetch_AutoIndexesOnFirstUse(t *testing.T) {
	dir := fetchFixture(t)
	// runInit doesn't build the index. The fetch path should detect an
	// empty index and rebuild on first call.
	resp, err := runFetch(context.Background(), fetchOptions{
		Root:  dir,
		Query: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Context == "" || !strings.Contains(resp.Context, "Refresh") {
		t.Errorf("auto-rebuild didn't populate the index:\n%s", resp.Context)
	}
}
