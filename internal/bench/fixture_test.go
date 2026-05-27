package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
)

// TestBuildBenchProject_SmallSize_ProducesParseableTree confirms the
// fixture generator doesn't drift into producing unparseable Markdown
// or wrong category counts. Running this alongside the benchmarks
// keeps the harness honest.
func TestBuildBenchProject_SmallSize_ProducesParseableTree(t *testing.T) {
	root := BuildBenchProject(t, SmallFixtureSize())
	memDir := filepath.Join(root, ".agent-memory")

	// Required files all exist.
	for _, rel := range []string{
		"meta/manifest.yaml",
		"meta/schema.yaml",
		"meta/lock",
		".gitignore",
		"index.md",
		"conventions.md",
		"decisions.md",
		"pitfalls.md",
	} {
		if _, err := os.Stat(filepath.Join(memDir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	// Module + archive counts match request.
	mods, _ := filepath.Glob(filepath.Join(memDir, "modules", "*.md"))
	if got, want := len(mods), SmallFixtureSize().Modules; got != want {
		t.Errorf("modules count = %d, want %d", got, want)
	}
	archives, _ := filepath.Glob(filepath.Join(memDir, "archive", "*.md"))
	if got, want := len(archives), SmallFixtureSize().ArchiveFiles; got != want {
		t.Errorf("archive count = %d, want %d", got, want)
	}

	// Decisions / pitfalls parse and contain the expected section count.
	for _, c := range []struct {
		path     string
		want     int
		idPrefix string
	}{
		{"decisions.md", SmallFixtureSize().DecisionSections + 1, "decision"},
		{"pitfalls.md", SmallFixtureSize().PitfallSections + 1, "pitfall"},
	} {
		body, err := os.ReadFile(filepath.Join(memDir, c.path))
		if err != nil {
			t.Fatal(err)
		}
		sections, err := agentmd.ParseSections(body)
		if err != nil {
			t.Errorf("%s: parse: %v", c.path, err)
			continue
		}
		// +1 = the top-level "# Decisions" / "# Pitfalls" heading.
		if len(sections) != c.want {
			t.Errorf("%s: sections = %d, want %d", c.path, len(sections), c.want)
		}
		// Every nested section has an anchor ID matching the prefix.
		matchingAnchors := 0
		for _, sec := range sections {
			if strings.HasPrefix(sec.AnchorID, c.idPrefix+"-") {
				matchingAnchors++
			}
		}
		if matchingAnchors != c.want-1 {
			t.Errorf("%s: %s-prefixed anchors = %d, want %d",
				c.path, c.idPrefix, matchingAnchors, c.want-1)
		}
	}
}

func TestBuildBenchProject_DeterministicAcrossRuns(t *testing.T) {
	// Two runs with the same FixtureSize must produce byte-identical
	// content for at least one well-known file. The output dir is
	// different (t.TempDir() per call), so we compare a representative
	// file across two calls.
	rootA := BuildBenchProject(t, DefaultFixtureSize())
	rootB := BuildBenchProject(t, DefaultFixtureSize())

	for _, rel := range []string{"decisions.md", "modules/module-005.md", "conventions.md"} {
		a, err := os.ReadFile(filepath.Join(rootA, ".agent-memory", rel))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(rootB, ".agent-memory", rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs across runs (fixture not deterministic)\n--- A ---\n%s\n--- B ---\n%s",
				rel, a, b)
		}
	}
}
