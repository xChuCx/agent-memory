package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/schema"
)

// ---------- sqlite.go ----------

func openTestIndex(t *testing.T) (*Index, context.Context) {
	t.Helper()
	dir := t.TempDir()
	idx, err := Open(filepath.Join(dir, "index.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	ctx := context.Background()
	if err := idx.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return idx, ctx
}

func TestOpenInitVersion(t *testing.T) {
	idx, ctx := openTestIndex(t)
	v, err := idx.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != SchemaVersion {
		t.Errorf("Version = %d, want %d", v, SchemaVersion)
	}
}

func TestInitIdempotent(t *testing.T) {
	idx, ctx := openTestIndex(t)
	// Running Init twice on an open DB must not error (IF NOT EXISTS).
	if err := idx.Init(ctx); err != nil {
		t.Errorf("second Init: %v", err)
	}
}

func TestCloseIdempotent(t *testing.T) {
	idx, _ := openTestIndex(t)
	if err := idx.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := idx.Close(); err != nil {
		t.Errorf("second Close should be a no-op: %v", err)
	}
}

// ---------- incremental.go ----------

func sectionDoc(file, id, heading, content string) SectionDoc {
	return SectionDoc{
		File:         file,
		SectionID:    id,
		Heading:      heading,
		HeadingLevel: 2,
		Title:        heading,
		Headings:     heading,
		Content:      content,
		Tags:         "",
		ByteStart:    0,
		ByteEnd:      len(content),
		ContentHash:  "sha256:fake",
	}
}

func TestUpsertSections_Insert(t *testing.T) {
	idx, ctx := openTestIndex(t)
	docs := []SectionDoc{
		sectionDoc("modules/auth.md", "token-rotation", "Token Rotation", "Refresh tokens rotate on each use."),
		sectionDoc("decisions.md", "use-postgres", "Use Postgres", "We chose Postgres for transactional storage."),
	}
	if err := idx.UpsertSections(ctx, docs); err != nil {
		t.Fatal(err)
	}
	n, _ := idx.CountSections(ctx)
	if n != 2 {
		t.Errorf("CountSections = %d, want 2", n)
	}
}

func TestUpsertSections_Update_NoDuplicate(t *testing.T) {
	idx, ctx := openTestIndex(t)
	first := sectionDoc("modules/auth.md", "token-rotation", "Token Rotation", "original body")
	if err := idx.UpsertSections(ctx, []SectionDoc{first}); err != nil {
		t.Fatal(err)
	}
	second := sectionDoc("modules/auth.md", "token-rotation", "Token Rotation", "updated body with marker xyzzy")
	if err := idx.UpsertSections(ctx, []SectionDoc{second}); err != nil {
		t.Fatal(err)
	}
	n, _ := idx.CountSections(ctx)
	if n != 1 {
		t.Errorf("CountSections = %d, want 1 (upsert, not duplicate)", n)
	}
	// New content is searchable.
	results, err := idx.Search(ctx, "xyzzy", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].SectionID != "token-rotation" {
		t.Errorf("SectionID = %q, want token-rotation", results[0].SectionID)
	}
}

func TestUpsertSections_EmptyIsNoop(t *testing.T) {
	idx, ctx := openTestIndex(t)
	if err := idx.UpsertSections(ctx, nil); err != nil {
		t.Errorf("empty upsert errored: %v", err)
	}
	n, _ := idx.CountSections(ctx)
	if n != 0 {
		t.Errorf("CountSections = %d after empty upsert", n)
	}
}

func TestDeleteSections(t *testing.T) {
	idx, ctx := openTestIndex(t)
	docs := []SectionDoc{
		sectionDoc("modules/auth.md", "a", "A", "alpha"),
		sectionDoc("modules/auth.md", "b", "B", "beta"),
	}
	_ = idx.UpsertSections(ctx, docs)

	if err := idx.DeleteSections(ctx, "modules/auth.md", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	n, _ := idx.CountSections(ctx)
	if n != 1 {
		t.Errorf("CountSections = %d, want 1", n)
	}
	if _, err := idx.GetSection(ctx, "modules/auth.md", "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if _, err := idx.GetSection(ctx, "modules/auth.md", "b"); err != nil {
		t.Errorf("b should still exist: %v", err)
	}
}

func TestDeleteFile_WipesAllRows(t *testing.T) {
	idx, ctx := openTestIndex(t)
	_ = idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("modules/auth.md", "a", "A", "x"),
		sectionDoc("modules/auth.md", "b", "B", "y"),
		sectionDoc("decisions.md", "d", "D", "z"),
	})
	_ = idx.UpsertFile(ctx, FileDoc{File: "modules/auth.md", Category: "modules", GitTracked: true})
	_ = idx.UpsertFile(ctx, FileDoc{File: "decisions.md", Category: "decisions", GitTracked: true})

	if err := idx.DeleteFile(ctx, "modules/auth.md"); err != nil {
		t.Fatal(err)
	}
	if n, _ := idx.CountSections(ctx); n != 1 {
		t.Errorf("sections remaining = %d, want 1", n)
	}
	if n, _ := idx.CountFiles(ctx); n != 1 {
		t.Errorf("files remaining = %d, want 1", n)
	}
}

func TestUpsertFile_RoundTrip(t *testing.T) {
	idx, ctx := openTestIndex(t)
	doc := FileDoc{
		File:         "decisions.md",
		Category:     "decisions",
		Freshness:    "fresh",
		Confidence:   "confirmed",
		LastModified: "2026-05-26T10:00:00Z",
		Committed:    true,
		LocalState:   false,
		Archived:     false,
		SizeBytes:    1024,
		Checksum:     "sha256:abc",
	}
	if err := idx.UpsertFile(ctx, doc); err != nil {
		t.Fatal(err)
	}
	got, err := idx.GetFile(ctx, "decisions.md")
	if err != nil {
		t.Fatal(err)
	}
	if got.Category != "decisions" {
		t.Errorf("Category: got %q, want decisions", got.Category)
	}
	if !got.Committed {
		t.Error("Committed lost in round-trip")
	}
	if got.SizeBytes != 1024 {
		t.Errorf("SizeBytes: got %d, want 1024", got.SizeBytes)
	}
}

func TestGetSection_NotFound(t *testing.T) {
	idx, ctx := openTestIndex(t)
	_, err := idx.GetSection(ctx, "nope.md", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetFile_NotFound(t *testing.T) {
	idx, ctx := openTestIndex(t)
	_, err := idx.GetFile(ctx, "nope.md")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// ---------- query.go ----------

func TestSearch_RanksByRelevance(t *testing.T) {
	idx, ctx := openTestIndex(t)
	// One section that's a strong match for "refresh token", one weaker.
	docs := []SectionDoc{
		sectionDoc("modules/auth.md", "strong",
			"Refresh Token Rotation",
			"Refresh token rotation is the canonical pattern. Refresh tokens rotate on every successful use."),
		sectionDoc("modules/auth.md", "weak",
			"Configuration",
			"This section mentions a token only once, in passing."),
	}
	_ = idx.UpsertSections(ctx, docs)

	results, err := idx.Search(ctx, "refresh token", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if results[0].SectionID != "strong" {
		t.Errorf("top result SectionID = %q, want strong", results[0].SectionID)
	}
}

func TestSearch_EmptyQueryReturnsNothing(t *testing.T) {
	idx, ctx := openTestIndex(t)
	_ = idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("decisions.md", "d", "D", "stuff"),
	})
	results, err := idx.Search(ctx, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("empty query returned %d results, want 0", len(results))
	}
}

func TestSearch_Snippet(t *testing.T) {
	idx, ctx := openTestIndex(t)
	_ = idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("modules/auth.md", "id1", "X",
			"The authentication subsystem checks a token before granting access."),
	})
	results, err := idx.Search(ctx, "authentication", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if !strings.Contains(results[0].Snippet, "[") || !strings.Contains(results[0].Snippet, "]") {
		t.Errorf("snippet missing match markers: %q", results[0].Snippet)
	}
}

func TestListSections_OrderedByByteStart(t *testing.T) {
	idx, ctx := openTestIndex(t)
	docs := []SectionDoc{
		{File: "x.md", SectionID: "c", Heading: "C", HeadingLevel: 2, Title: "C", Content: "c", ByteStart: 200, ByteEnd: 300},
		{File: "x.md", SectionID: "a", Heading: "A", HeadingLevel: 2, Title: "A", Content: "a", ByteStart: 0, ByteEnd: 100},
		{File: "x.md", SectionID: "b", Heading: "B", HeadingLevel: 2, Title: "B", Content: "b", ByteStart: 100, ByteEnd: 200},
	}
	_ = idx.UpsertSections(ctx, docs)

	got, err := idx.ListSections(ctx, "x.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sections, want 3", len(got))
	}
	wantIDs := []string{"a", "b", "c"}
	for i, w := range wantIDs {
		if got[i].SectionID != w {
			t.Errorf("position %d: got %q, want %q", i, got[i].SectionID, w)
		}
	}
}

func TestListFiles_CategoryFilter(t *testing.T) {
	idx, ctx := openTestIndex(t)
	_ = idx.UpsertFile(ctx, FileDoc{File: "modules/a.md", Category: "modules"})
	_ = idx.UpsertFile(ctx, FileDoc{File: "modules/b.md", Category: "modules"})
	_ = idx.UpsertFile(ctx, FileDoc{File: "decisions.md", Category: "decisions"})

	all, err := idx.ListFiles(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("all files: got %d, want 3", len(all))
	}

	modules, err := idx.ListFiles(ctx, "modules")
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 {
		t.Errorf("modules files: got %d, want 2", len(modules))
	}
	for _, f := range modules {
		if f.Category != "modules" {
			t.Errorf("category filter leaked %s", f.Category)
		}
	}
}

// ---------- rebuild.go ----------

// scaffoldMemory creates a minimal .agent-memory/ layout for rebuild tests:
// one anchored module, one decisions file, one current-state local file.
func scaffoldMemory(t *testing.T) (memDir string, sch *schema.Schema) {
	t.Helper()
	root := t.TempDir()
	memDir = filepath.Join(root, ".agent-memory")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{"modules", "local"} {
		must(os.MkdirAll(filepath.Join(memDir, d), 0755))
	}
	must(os.WriteFile(filepath.Join(memDir, "decisions.md"),
		[]byte("# Decisions\n<!-- @id: decisions -->\n\n## Use Postgres\n<!-- @id: use-postgres -->\n\nChosen for transactional storage.\n"),
		0644))
	must(os.WriteFile(filepath.Join(memDir, "modules", "auth.md"),
		[]byte("## Token Rotation\n<!-- @id: token-rotation -->\n\nRefresh tokens rotate on each use.\n"),
		0644))
	must(os.WriteFile(filepath.Join(memDir, "local", "current.main.md"),
		[]byte("## Active Task\n\nrefactor auth\n"),
		0644))

	return memDir, schema.DefaultSchema()
}

func TestRebuildAll_IndexesAllCategorisedFiles(t *testing.T) {
	memDir, sch := scaffoldMemory(t)
	idx, ctx := openTestIndex(t)

	if err := idx.RebuildAll(ctx, memDir, sch, RebuildOpts{}); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	// decisions.md has 2 sections (h1 + h2); auth.md has 1 section; current.main.md
	// has 1 section but no @id, and "current" category has SectionIDRequired=false,
	// so it gets indexed with empty SectionID — but our scaffold has SectionIDRequired
	// false for current, so the empty-anchor section is included.
	n, _ := idx.CountSections(ctx)
	if n < 3 {
		t.Errorf("CountSections = %d, want >= 3", n)
	}

	// Files row count: 3.
	nf, _ := idx.CountFiles(ctx)
	if nf != 3 {
		t.Errorf("CountFiles = %d, want 3", nf)
	}

	// modules/auth.md → category "modules".
	f, err := idx.GetFile(ctx, "modules/auth.md")
	if err != nil {
		t.Fatal(err)
	}
	if f.Category != "modules" {
		t.Errorf("Category = %q, want modules", f.Category)
	}

	// Search for the body text we wrote.
	results, _ := idx.Search(ctx, "refresh", 5)
	if len(results) == 0 {
		t.Error("expected 'refresh' to match the auth section")
	}
}

func TestRebuildAll_WipesPreviousState(t *testing.T) {
	memDir, sch := scaffoldMemory(t)
	idx, ctx := openTestIndex(t)

	// Seed with a stale row.
	_ = idx.UpsertSections(ctx, []SectionDoc{
		sectionDoc("stale.md", "stale-id", "Stale", "should be wiped"),
	})

	if err := idx.RebuildAll(ctx, memDir, sch, RebuildOpts{}); err != nil {
		t.Fatal(err)
	}

	if _, err := idx.GetSection(ctx, "stale.md", "stale-id"); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale section survived rebuild: %v", err)
	}
}

func TestRebuildAll_AssignMissingIDs(t *testing.T) {
	memDir, sch := scaffoldMemory(t)
	// Add a module file WITHOUT any @id anchors.
	noAnchorPath := filepath.Join(memDir, "modules", "payments.md")
	noAnchorContent := []byte("## Stripe Integration\n\nWe use Stripe.\n\n## Webhooks\n\nWith idempotency keys.\n")
	if err := os.WriteFile(noAnchorPath, noAnchorContent, 0644); err != nil {
		t.Fatal(err)
	}

	idx, ctx := openTestIndex(t)
	if err := idx.RebuildAll(ctx, memDir, sch, RebuildOpts{AssignMissingIDs: true}); err != nil {
		t.Fatal(err)
	}

	// After the rebuild, the file should now contain @id anchors.
	updated, err := os.ReadFile(noAnchorPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "<!-- @id:") {
		t.Errorf("AssignMissingIDs did not annotate payments.md:\n%s", updated)
	}

	// The two new sections should be in the index.
	results, _ := idx.Search(ctx, "stripe", 5)
	if len(results) == 0 {
		t.Error("expected stripe section indexed after AssignMissingIDs")
	}
}

func TestRebuildAll_SkipsUnclassifiedFiles(t *testing.T) {
	memDir, sch := scaffoldMemory(t)
	// Write a Markdown file that no schema category matches.
	strayPath := filepath.Join(memDir, "stray.md")
	if err := os.WriteFile(strayPath, []byte("## Stray\n<!-- @id: stray -->\n\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}

	idx, ctx := openTestIndex(t)
	if err := idx.RebuildAll(ctx, memDir, sch, RebuildOpts{}); err != nil {
		t.Fatal(err)
	}

	if _, err := idx.GetFile(ctx, "stray.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("stray.md should not have been indexed: %v", err)
	}
}
