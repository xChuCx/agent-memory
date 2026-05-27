// Package bench holds the benchmark harness for the M8 milestone:
// a deterministic fixture generator that builds a realistic
// .agent-memory/ tree, plus end-to-end benchmarks for the hot paths
// (fetch_context, propose_update, RebuildAll).
//
// Run with:
//
//	go test -bench=. -benchmem ./internal/bench/...
//
// See docs/bench-harness.md for interpretation guidance and current
// baseline numbers.
package bench

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// FixtureSize controls how big a corpus BuildBenchProject generates.
// Counts are upper bounds; the generator may emit fewer items if a
// requested size is unrealistic for a category (e.g., negative).
type FixtureSize struct {
	// Modules is the number of files under modules/.
	Modules int
	// DecisionSections is the number of `## Heading` sections inside
	// the single decisions.md file.
	DecisionSections int
	// PitfallSections is the number of sections inside pitfalls.md.
	PitfallSections int
	// ArchiveFiles is the number of files under archive/.
	ArchiveFiles int
	// SectionBodyChars is the target length (in characters) of each
	// generated section body. Real-world files vary widely; this is
	// "medium prose" by default.
	SectionBodyChars int
}

// DefaultFixtureSize returns a "realistic medium project" set of
// counts. Total ~120 sections across ~70 files, ~50 KB of Markdown.
func DefaultFixtureSize() FixtureSize {
	return FixtureSize{
		Modules:          20,
		DecisionSections: 20,
		PitfallSections:  50,
		ArchiveFiles:     30,
		SectionBodyChars: 400,
	}
}

// SmallFixtureSize is for quick "does it crash" benchmarks during
// development. Roughly an order of magnitude smaller than default.
func SmallFixtureSize() FixtureSize {
	return FixtureSize{
		Modules:          3,
		DecisionSections: 3,
		PitfallSections:  5,
		ArchiveFiles:     3,
		SectionBodyChars: 200,
	}
}

// LargeFixtureSize stresses the index + parser. Total ~600 sections
// across ~250 files — heavier than most real projects.
func LargeFixtureSize() FixtureSize {
	return FixtureSize{
		Modules:          100,
		DecisionSections: 100,
		PitfallSections:  200,
		ArchiveFiles:     150,
		SectionBodyChars: 600,
	}
}

// BuildBenchProject writes a complete .agent-memory/ tree under
// tb.TempDir() and returns the project root. The tree is byte-
// deterministic for a given size — same input produces same on-disk
// state, so bench results are stable across runs.
//
// Layout:
//
//	<root>/
//	  .agent-memory/
//	    meta/{manifest.yaml,schema.yaml,lock}
//	    .gitignore
//	    index.md
//	    conventions.md
//	    decisions.md          (size.DecisionSections sections)
//	    pitfalls.md           (size.PitfallSections sections)
//	    modules/N.md          (size.Modules files, one section each)
//	    archive/2026-NN-...md (size.ArchiveFiles files, one section each)
//	    local/                (empty)
//	    sessions/             (empty)
//	    staging/              (empty)
//
// Tests get parseability + count guarantees; benchmarks get a stable
// corpus that exercises every category. tb is testing.TB so the same
// helper works for both *testing.T and *testing.B.
func BuildBenchProject(tb testing.TB, size FixtureSize) string {
	tb.Helper()

	root := tb.TempDir()
	memDir := filepath.Join(root, ".agent-memory")

	mkdir := func(rel string) {
		tb.Helper()
		if err := os.MkdirAll(filepath.Join(memDir, rel), 0755); err != nil {
			tb.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	for _, sub := range []string{"meta", "modules", "archive", "local", "sessions", "staging"} {
		mkdir(sub)
	}

	// Manifest + schema use the default values. Default manifest has
	// staging.ttl_seconds=604800 and routing policy that stages durable
	// updates — both fine for benchmarks.
	if err := schema.WriteDefault(filepath.Join(memDir, "meta", "schema.yaml")); err != nil {
		tb.Fatalf("schema: %v", err)
	}
	if err := config.WriteDefault(filepath.Join(memDir, "meta", "manifest.yaml"), "bench-project"); err != nil {
		tb.Fatalf("manifest: %v", err)
	}

	// Empty lock file so meta/ has the canonical layout. lock.Acquire
	// would create this lazily, but we set it up so doctor doesn't
	// complain on the fixture either.
	if err := os.WriteFile(filepath.Join(memDir, "meta", "lock"), nil, 0644); err != nil {
		tb.Fatal(err)
	}

	// .gitignore matches what `agent-memory init` produces.
	gitignore := "local/\nsessions/\nmeta/index.sqlite*\nmeta/lock\nstaging/\n"
	if err := os.WriteFile(filepath.Join(memDir, ".gitignore"), []byte(gitignore), 0644); err != nil {
		tb.Fatal(err)
	}

	// Durable files. Use a single deterministic PRNG seeded with 0 so
	// the corpus is byte-identical across runs.
	rng := rand.New(rand.NewSource(0))

	write := func(rel string, body []byte) {
		tb.Helper()
		if err := os.WriteFile(filepath.Join(memDir, filepath.FromSlash(rel)), body, 0644); err != nil {
			tb.Fatalf("write %s: %v", rel, err)
		}
	}

	write("index.md", []byte("# Agent Memory Index\n<!-- @generated -->\n\nbench fixture index.\n"))
	write("conventions.md",
		buildConventions(rng, size))
	write("decisions.md",
		buildSectioned(rng, "Decisions", "decision", size.DecisionSections, size.SectionBodyChars, true))
	write("pitfalls.md",
		buildSectioned(rng, "Pitfalls", "pitfall", size.PitfallSections, size.SectionBodyChars, true))

	for i := 0; i < size.Modules; i++ {
		name := fmt.Sprintf("module-%03d", i)
		body := buildModuleFile(rng, name, size.SectionBodyChars)
		write("modules/"+name+".md", body)
	}

	for i := 0; i < size.ArchiveFiles; i++ {
		name := fmt.Sprintf("2026-%02d-archived-%03d", (i%12)+1, i)
		body := buildArchiveFile(rng, name, size.SectionBodyChars)
		write("archive/"+name+".md", body)
	}

	return root
}

// loadDeps returns the manifest + schema + open + initialised + warmed
// FTS index for a built fixture. Bench helper — not for production use.
func LoadDeps(tb testing.TB, root string) (*config.Manifest, *schema.Schema, *index.Index) {
	tb.Helper()
	memDir := filepath.Join(root, ".agent-memory")

	mf, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		tb.Fatalf("LoadDeps: manifest: %v", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		tb.Fatalf("LoadDeps: schema: %v", err)
	}
	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		tb.Fatalf("LoadDeps: open index: %v", err)
	}
	if err := idx.Init(context.Background()); err != nil {
		tb.Fatalf("LoadDeps: init index: %v", err)
	}
	// Pre-build so query benches don't pay the rebuild cost on first call.
	if err := idx.RebuildAll(context.Background(), memDir, sch, index.RebuildOpts{
		AssignMissingIDs: false, // fixtures already have IDs
	}); err != nil {
		tb.Fatalf("LoadDeps: rebuild index: %v", err)
	}
	tb.Cleanup(func() { _ = idx.Close() })
	return mf, sch, idx
}

// =============================================================================
// Content generators (deterministic via the rng we hand in)
// =============================================================================

// vocabulary is the lorem-ipsum-like pool. Real-world content varies
// far more wildly; this is just enough to:
//   - vary section bodies across iterations so the FTS index has
//     interesting term distributions
//   - keep parser / scanner / indexer doing real work, not optimised-
//     away no-ops
var vocabulary = []string{
	"the", "system", "must", "handle", "retry", "with", "exponential",
	"backoff", "when", "the", "upstream", "service", "returns", "five",
	"hundred", "errors", "session", "tokens", "rotate", "on", "every",
	"successful", "request", "validation", "happens", "in", "the",
	"middleware", "layer", "before", "the", "handler", "sees", "the",
	"request", "tracing", "spans", "carry", "the", "tenant", "id",
	"propagated", "via", "header", "logging", "uses", "structured",
	"json", "with", "the", "request", "id", "as", "the", "correlation",
	"key", "queries", "go", "through", "the", "prepared", "statement",
	"cache", "to", "amortise", "parser", "cost", "caches", "expire",
	"after", "five", "minutes", "and", "lazy", "revalidate", "on",
	"next", "fetch", "errors", "bubble", "up", "wrapped", "with",
	"context", "and", "logged", "at", "warn", "level", "metrics", "are",
	"exported", "via", "prometheus", "on", "port", "nine", "ninety",
	"and", "scraped", "every", "fifteen", "seconds", "by", "the",
	"observability", "stack",
}

func buildConventions(rng *rand.Rand, size FixtureSize) []byte {
	var b strings.Builder
	b.WriteString("# Conventions\n<!-- @id: conventions -->\n\n")
	b.WriteString(fillBody(rng, size.SectionBodyChars))
	b.WriteString("\n")
	return []byte(b.String())
}

// buildSectioned builds a multi-section file under a top-level
// heading. Each section has an @id anchor so the index sees it.
func buildSectioned(
	rng *rand.Rand,
	topHeading, idPrefix string,
	n, bodyChars int,
	withConventionFields bool,
) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n<!-- @id: %s -->\n\n", topHeading, slug(topHeading))
	b.WriteString("Top-level intro paragraph.\n\n")
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-%03d", idPrefix, i)
		fmt.Fprintf(&b, "## %s %03d\n<!-- @id: %s -->\n\n", topHeading[:len(topHeading)-1], i, id)
		if withConventionFields {
			fmt.Fprintf(&b, "Date: 2026-05-%02d\nStatus: active\nConfidence: confirmed\n\n", (i%28)+1)
		}
		b.WriteString(fillBody(rng, bodyChars))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func buildModuleFile(rng *rand.Rand, name string, bodyChars int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n<!-- @id: %s -->\n\n", name, name)
	fmt.Fprintf(&b, "## Overview\n<!-- @id: %s-overview -->\n\n", name)
	b.WriteString(fillBody(rng, bodyChars))
	b.WriteString("\n")
	fmt.Fprintf(&b, "## Internals\n<!-- @id: %s-internals -->\n\n", name)
	b.WriteString(fillBody(rng, bodyChars))
	b.WriteString("\n")
	return []byte(b.String())
}

func buildArchiveFile(rng *rand.Rand, name string, bodyChars int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Archived: %s\n<!-- @id: %s -->\n\n", name, name)
	fmt.Fprintf(&b, "Archived-At: 2026-%s-%02dT00:00:00Z\n\n", name[5:7], 1)
	b.WriteString(fillBody(rng, bodyChars))
	b.WriteString("\n")
	return []byte(b.String())
}

// fillBody emits roughly approxChars characters of vocabulary text,
// broken into ~80-column lines. Uses the seeded rng so output is
// deterministic.
func fillBody(rng *rand.Rand, approxChars int) string {
	var b strings.Builder
	var line strings.Builder
	for b.Len()+line.Len() < approxChars {
		w := vocabulary[rng.Intn(len(vocabulary))]
		if line.Len()+len(w)+1 > 80 {
			b.WriteString(line.String())
			b.WriteByte('\n')
			line.Reset()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(w)
	}
	if line.Len() > 0 {
		b.WriteString(line.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// slug downcases s and replaces non-alphanumerics with dashes. Used to
// derive @id values from heading text.
func slug(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}
