package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xChuCx/agent-memory/internal/schema"
)

// ---------- federation store dimension (PR4) ----------

func upsertOne(t *testing.T, idx *Index, ctx context.Context, store, file, sid, content string) {
	t.Helper()
	if err := idx.UpsertSections(ctx, []SectionDoc{{
		Store:     store,
		File:      file,
		SectionID: sid,
		Heading:   content,
		Title:     content,
		Content:   content,
	}}); err != nil {
		t.Fatalf("UpsertSections(%s:%s/%s): %v", store, file, sid, err)
	}
}

func mustSearch(t *testing.T, idx *Index, ctx context.Context, q string) []SearchResult {
	t.Helper()
	r, err := idx.Search(ctx, q, 50)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return r
}

func writeStoreFile(t *testing.T, base, rel, content string) {
	t.Helper()
	p := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Init drops and recreates the schema when the on-disk index was written by an
// older version (rebuild-on-version-bump). The migrated index is empty and uses
// the current (store-aware) schema; the caller repopulates it.
func TestInit_RebuildOnVersionBump(t *testing.T) {
	idx, ctx := openTestIndex(t) // fresh index at the current SchemaVersion
	upsertOne(t, idx, ctx, LocalStore, "decisions.md", "d1", "stale row before bump")
	if n, _ := idx.CountSections(ctx); n == 0 {
		t.Fatal("precondition: expected a row before the simulated bump")
	}

	// Simulate an older on-disk schema version. The migration logic keys off
	// PRAGMA user_version, not the actual column set, so this faithfully
	// exercises the "stored version != current" path.
	if _, err := idx.db.ExecContext(ctx, "PRAGMA user_version=1"); err != nil {
		t.Fatalf("set stale version: %v", err)
	}

	// Re-Init sees the gap, drops the stale tables, and recreates the current
	// schema empty.
	if err := idx.Init(ctx); err != nil {
		t.Fatalf("migrating Init: %v", err)
	}
	if v, _ := idx.Version(ctx); v != SchemaVersion {
		t.Fatalf("after migration Version = %d, want %d", v, SchemaVersion)
	}
	if n, _ := idx.CountSections(ctx); n != 0 {
		t.Fatalf("stale rows survived the migration: %d", n)
	}

	// The recreated schema is store-aware: a store-keyed row inserts and is
	// retrievable per store.
	upsertOne(t, idx, ctx, "platform", "contracts.md", "c1", "afterbump token")
	res, err := idx.SearchPerStore(ctx, "afterbump", 5, []string{"platform"})
	if err != nil {
		t.Fatalf("SearchPerStore after migration: %v", err)
	}
	if len(res) != 1 || res[0].Store != "platform" {
		t.Fatalf("expected 1 platform hit after migration, got %+v", res)
	}
}

// Search stays scoped to the local store (pre-federation behavior), while
// SearchPerStore retrieves a fair per-store top-K. The composite key
// (store, file, section_id) lets the same (file, section_id) coexist across
// stores.
func TestSearchPerStore_FairAndKeyed(t *testing.T) {
	idx, ctx := openTestIndex(t)

	upsertOne(t, idx, ctx, LocalStore, "decisions.md", "d1", "token rotation local")
	upsertOne(t, idx, ctx, "platform", "contracts.md", "c1", "token rotation platform alpha")
	upsertOne(t, idx, ctx, "platform", "contracts.md", "c2", "token rotation platform beta")
	upsertOne(t, idx, ctx, "platform", "contracts.md", "c3", "token rotation platform gamma")
	// Same (file, section_id) as platform/c1 — must coexist under a different store.
	upsertOne(t, idx, ctx, "shared", "contracts.md", "c1", "token rotation shared")

	// Search is local-only: cross-store rows never leak into the fetch path.
	local, err := idx.Search(ctx, "token", 50)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(local) != 1 || local[0].Store != LocalStore {
		t.Fatalf("Search should return only the local row, got %+v", local)
	}

	// Per-store cap: kPerStore=1 yields at most one hit per named store.
	fair, err := idx.SearchPerStore(ctx, "token", 1, []string{"platform", "shared"})
	if err != nil {
		t.Fatalf("SearchPerStore: %v", err)
	}
	byStore := map[string]int{}
	for _, r := range fair {
		byStore[r.Store]++
	}
	if len(fair) != 2 || byStore["platform"] != 1 || byStore["shared"] != 1 {
		t.Fatalf("kPerStore=1 fairness violated: %+v", fair)
	}

	// A larger cap surfaces all platform rows; platform/c1 and shared/c1 both survive.
	all, err := idx.SearchPerStore(ctx, "token", 10, []string{"platform", "shared", LocalStore})
	if err != nil {
		t.Fatalf("SearchPerStore (all): %v", err)
	}
	byStore = map[string]int{}
	for _, r := range all {
		byStore[r.Store]++
	}
	if byStore["platform"] != 3 || byStore["shared"] != 1 || byStore[LocalStore] != 1 {
		t.Fatalf("expected 3 platform + 1 shared + 1 local, got %+v (n=%d)", byStore, len(all))
	}

	// Unknown store → no rows; empty/nil store list → nil.
	if r, _ := idx.SearchPerStore(ctx, "token", 5, []string{"ghost"}); len(r) != 0 {
		t.Fatalf("unknown store should yield no rows, got %+v", r)
	}
	if r, _ := idx.SearchPerStore(ctx, "token", 5, nil); r != nil {
		t.Fatalf("nil stores should yield nil, got %+v", r)
	}
}

// RebuildAll indexes the local tree under LocalStore and each cached external
// store under meta/cache/stores/<name>/ under its own name. Cached content is
// retrievable via SearchPerStore but never via the local-scoped Search — so
// fetch behavior is unchanged until PR5 opts in.
func TestRebuildAll_LocalAndCachedStores(t *testing.T) {
	idx, ctx := openTestIndex(t)
	memDir := t.TempDir()
	writeStoreFile(t, memDir, "decisions.md",
		"# Decisions\n\n## Adopt zorptoken auth\n<!-- @id: d-zorp -->\n- Decision: adopt zorptoken\n- Status: accepted\n")
	// A materialised external store (as PR3 sync would produce).
	writeStoreFile(t, memDir, "meta/cache/stores/platform/decisions.md",
		"# Decisions\n\n## Platformwidget rollout\n<!-- @id: d-pw -->\n- Decision: ship platformwidget\n- Status: accepted\n")

	if err := idx.RebuildAll(ctx, memDir, schema.DefaultSchema(), RebuildOpts{AssignMissingIDs: true}); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}

	// Local content is found by the local-scoped Search; every hit is tagged
	// local. (A token can match more than one section — the H1 byte range wraps
	// its H2 children — so we assert attribution, not an exact count.)
	local := mustSearch(t, idx, ctx, "zorptoken")
	if len(local) == 0 {
		t.Fatal("local Search for zorptoken returned nothing")
	}
	for _, r := range local {
		if r.Store != LocalStore {
			t.Fatalf("local Search returned a non-local hit: %+v", r)
		}
	}
	// Cached content must NOT leak into the local Search (opt-in invariant).
	if r, _ := idx.Search(ctx, "platformwidget", 5); len(r) != 0 {
		t.Fatalf("cached content leaked into local Search: %+v", r)
	}
	// But it IS indexed under its store name.
	cached, err := idx.SearchPerStore(ctx, "platformwidget", 5, []string{"platform"})
	if err != nil {
		t.Fatalf("SearchPerStore(platform): %v", err)
	}
	if len(cached) == 0 {
		t.Fatal("cached SearchPerStore for platformwidget returned nothing")
	}
	for _, r := range cached {
		if r.Store != "platform" {
			t.Fatalf("cached SearchPerStore returned a non-platform hit: %+v", r)
		}
	}

	// ListFiles / GetFile are local-only: the cached decisions.md is excluded
	// even though it shares the relative path.
	files, err := idx.ListFiles(ctx, "")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].File != "decisions.md" {
		t.Fatalf("ListFiles should return only the local decisions.md, got %+v", files)
	}
	if _, err := idx.GetFile(ctx, "decisions.md"); err != nil {
		t.Fatalf("GetFile(decisions.md) local: %v", err)
	}
}

// With no cached stores, RebuildAll indexes only the local tree — byte-for-byte
// the pre-federation behavior (the opt-in invariant).
func TestRebuildAll_NoCacheUnchanged(t *testing.T) {
	idx, ctx := openTestIndex(t)
	memDir := t.TempDir()
	writeStoreFile(t, memDir, "decisions.md",
		"# Decisions\n\n## Adopt zorptoken auth\n<!-- @id: d-zorp -->\n- Decision: adopt zorptoken\n- Status: accepted\n")

	if err := idx.RebuildAll(ctx, memDir, schema.DefaultSchema(), RebuildOpts{AssignMissingIDs: true}); err != nil {
		t.Fatalf("RebuildAll: %v", err)
	}
	if n, _ := idx.CountSections(ctx); n == 0 {
		t.Fatal("expected local sections to be indexed")
	}
	// No cache dir exists → any external store query is empty.
	if r, _ := idx.SearchPerStore(ctx, "zorptoken", 5, []string{"platform"}); len(r) != 0 {
		t.Fatalf("no cache dir, yet an external store returned rows: %+v", r)
	}
}
