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
)

// rebuildIndexFixture inits .agent-memory/ in a temp dir and drops a
// module file with an anchored section so the index has something to
// reflect. Returns the repo root.
func rebuildIndexFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "rebuild-test"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	modulesDir := filepath.Join(dir, ".agent-memory", "modules")
	if err := os.WriteFile(filepath.Join(modulesDir, "auth.md"),
		[]byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate.\n"),
		0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunRebuildIndex_HappyPathFromFreshInit(t *testing.T) {
	dir := rebuildIndexFixture(t)
	res, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root:      dir,
		AssignIDs: true,
	})
	if err != nil {
		t.Fatalf("runRebuildIndex: %v", err)
	}
	if res.FilesIndexed < 1 {
		t.Errorf("FilesIndexed = %d, want >= 1", res.FilesIndexed)
	}
	if res.SectionsIndexed < 1 {
		t.Errorf("SectionsIndexed = %d, want >= 1", res.SectionsIndexed)
	}
	// Index file landed on disk.
	if _, err := os.Stat(filepath.Join(dir, ".agent-memory", "meta", "index.sqlite")); err != nil {
		t.Errorf("index.sqlite not present after rebuild: %v", err)
	}
}

func TestRunRebuildIndex_Idempotent(t *testing.T) {
	dir := rebuildIndexFixture(t)
	first, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root:      dir,
		AssignIDs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root:      dir,
		AssignIDs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.FilesIndexed != second.FilesIndexed {
		t.Errorf("Files: first=%d second=%d (rebuild should be idempotent)",
			first.FilesIndexed, second.FilesIndexed)
	}
	if first.SectionsIndexed != second.SectionsIndexed {
		t.Errorf("Sections: first=%d second=%d", first.SectionsIndexed, second.SectionsIndexed)
	}
}

func TestRunRebuildIndex_ClobberRemovesFile(t *testing.T) {
	dir := rebuildIndexFixture(t)
	// First populate so meta/index.sqlite exists.
	if _, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root: dir, AssignIDs: true,
	}); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, ".agent-memory", "meta", "index.sqlite")
	// Verify file exists then call --clobber → it gets removed-then-recreated.
	statBefore, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	res, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root: dir, AssignIDs: true, Clobber: true,
	})
	if err != nil {
		t.Fatalf("clobber rebuild: %v", err)
	}
	if !res.Clobbered {
		t.Error("Clobbered flag not set in result")
	}
	statAfter, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("index.sqlite missing after clobber rebuild: %v", err)
	}
	// Inode/file identity changed (we can't easily compare inodes on
	// Windows, but mtime certainly should have moved).
	if !statAfter.ModTime().After(statBefore.ModTime()) {
		t.Errorf("clobber didn't recreate file (mtime unchanged: %v vs %v)",
			statBefore.ModTime(), statAfter.ModTime())
	}
}

func TestRunRebuildIndex_AssignIDsInjectsAnchors(t *testing.T) {
	dir := rebuildIndexFixture(t)
	// Add a module file without an @id anchor — modules require IDs per
	// DefaultSchema, so assign-ids should inject one.
	target := filepath.Join(dir, ".agent-memory", "modules", "billing.md")
	body := "## Subscription Lifecycle\n\nMonthly cycles ...\n"
	if err := os.WriteFile(target, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root: dir, AssignIDs: true,
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(target)
	if !strings.Contains(string(got), "<!-- @id:") {
		t.Errorf("expected anchor injected, got:\n%s", got)
	}
}

func TestRunRebuildIndex_NoAssignIDsPreservesMissing(t *testing.T) {
	dir := rebuildIndexFixture(t)
	target := filepath.Join(dir, ".agent-memory", "modules", "payments.md")
	body := "## Payouts\n\nPaid on the 1st.\n"
	if err := os.WriteFile(target, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root: dir, AssignIDs: false,
	}); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(target)
	if strings.Contains(string(got), "<!-- @id:") {
		t.Errorf("anchor injected despite --no-assign-ids:\n%s", got)
	}
}

func TestRunRebuildIndex_RejectsMissingAgentMemory(t *testing.T) {
	dir := t.TempDir() // no init
	_, err := runRebuildIndex(context.Background(), rebuildIndexOptions{
		Root: dir, AssignIDs: true,
	})
	if err == nil {
		t.Fatal("expected error for missing .agent-memory/")
	}
}

// =============================================================================
// Cobra integration
// =============================================================================

func TestCobra_RebuildIndexHumanOutput(t *testing.T) {
	dir := rebuildIndexFixture(t)
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rebuild-index", "--root", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rebuild-index: %v\n%s", err, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Index rebuilt in") {
		t.Errorf("stdout missing banner: %q", out)
	}
	if !strings.Contains(out, "files:") || !strings.Contains(out, "sections:") {
		t.Errorf("stdout missing counts: %q", out)
	}
}

func TestCobra_RebuildIndexJSONOutput(t *testing.T) {
	dir := rebuildIndexFixture(t)
	cmd := NewRootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"rebuild-index", "--root", dir, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rebuild-index --json: %v", err)
	}
	var res RebuildIndexResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, stdout.String())
	}
	if res.SectionsIndexed == 0 {
		t.Errorf("SectionsIndexed = 0; expected non-zero on a non-empty fixture")
	}
}
