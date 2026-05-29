package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/index"
)

func TestTokenize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string // expected set members (order irrelevant)
	}{
		{"empty", "", nil},
		{"lower+dedup", "Hello, WORLD! hello", []string{"hello", "world"}},
		{"punctuation-and-markdown", "<!-- @id: foo-bar -->", []string{"id", "foo", "bar"}},
		{"alnum-run", "test123 v2", []string{"test123", "v2"}},
		{"only-separators", "--- :: !! ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenize(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("tokenize(%q) size = %d, want %d (%v)", tc.in, len(got), len(tc.want), got)
			}
			for _, w := range tc.want {
				if _, ok := got[w]; !ok {
					t.Errorf("tokenize(%q) missing %q; got %v", tc.in, w, got)
				}
			}
		})
	}
}

func TestJaccardSimilarity(t *testing.T) {
	mk := func(words ...string) map[string]struct{} {
		s := make(map[string]struct{}, len(words))
		for _, w := range words {
			s[w] = struct{}{}
		}
		return s
	}
	cases := []struct {
		name string
		a, b map[string]struct{}
		want float64
	}{
		{"identical", mk("a", "b", "c"), mk("a", "b", "c"), 1.0},
		{"disjoint", mk("a", "b"), mk("c", "d"), 0.0},
		{"partial", mk("a", "b", "c", "d"), mk("c", "d", "e", "f"), 2.0 / 6.0},
		{"subset", mk("a", "b"), mk("a", "b", "c", "d"), 2.0 / 4.0},
		{"one-empty", mk(), mk("a"), 0.0},
		{"both-empty", mk(), mk(), 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jaccardSimilarity(tc.a, tc.b)
			if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("jaccardSimilarity = %v, want %v", got, tc.want)
			}
			// Symmetry.
			if rev := jaccardSimilarity(tc.b, tc.a); rev != got {
				t.Errorf("not symmetric: %v vs %v", got, rev)
			}
		})
	}
}

func TestIsNearDuplicate(t *testing.T) {
	accepted := []map[string]struct{}{
		tokenize("we cache auth tokens in redis with a five minute ttl and refresh on miss"),
	}
	// A one-word edit stays well above 0.85.
	if !isNearDuplicate(tokenize("we cache auth tokens in redis with a five minute ttl and refresh on read"), accepted) {
		t.Error("near-identical sentence not flagged as duplicate")
	}
	// Unrelated content is below threshold.
	if isNearDuplicate(tokenize("the build pipeline compiles static binaries without cgo"), accepted) {
		t.Error("unrelated sentence wrongly flagged as duplicate")
	}
	// Nothing accepted yet → never a duplicate.
	if isNearDuplicate(tokenize("anything at all"), nil) {
		t.Error("duplicate against empty accepted set")
	}
}

// TestBuildContextPack_DeduplicatesNearIdenticalSections is the end-to-end
// check: two sections with near-identical bodies in different files both
// match the query, but only one survives into the pack; the other is
// reported omitted as a near-duplicate. (Design §15.1 step 8 / §20.5 step 6.)
func TestBuildContextPack_DeduplicatesNearIdenticalSections(t *testing.T) {
	deps, cleanup := fixture(t)
	defer cleanup()

	body := "We cache auth tokens in Redis with a 5 minute TTL and refresh on miss.\n"
	write := func(rel, id string) {
		t.Helper()
		full := filepath.Join(deps.MemoryDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		content := "## Caching Strategy\n<!-- @id: " + id + " -->\n\n" + body
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("modules/cache_a.md", "caching-alpha")
	write("modules/cache_b.md", "caching-beta")

	ctx := context.Background()
	if err := deps.Idx.RebuildAll(ctx, deps.MemoryDir, deps.Schema, index.RebuildOpts{}); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}

	resp, err := BuildContextPack(ctx, FetchRequest{Query: "cache auth tokens redis ttl"}, deps)
	if err != nil {
		t.Fatalf("BuildContextPack: %v", err)
	}

	gotA := strings.Contains(resp.Context, "caching-alpha")
	gotB := strings.Contains(resp.Context, "caching-beta")
	if gotA == gotB {
		t.Fatalf("expected exactly one duplicate in the pack; gotA=%v gotB=%v\n%s", gotA, gotB, resp.Context)
	}

	foundDupOmit := false
	for _, o := range resp.Omitted {
		if strings.Contains(o.Reason, "near-duplicate") {
			foundDupOmit = true
		}
	}
	if !foundDupOmit {
		t.Errorf("expected a near-duplicate omission, got omitted=%+v", resp.Omitted)
	}
}
