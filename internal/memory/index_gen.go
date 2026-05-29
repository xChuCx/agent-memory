package memory

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentfs "github.com/agent-memory/agent-memory/internal/fs"
	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// indexFileName is the server-managed routing file. Agents cannot write
// to it through propose_update (the schema marks its category
// server_managed); the server regenerates it as a side effect of every
// durable apply.
const indexFileName = "index.md"

// indexGeneratedComment marks the file as machine-maintained. Kept
// verbatim so humans (and our own regeneration) recognise it.
const indexGeneratedComment = "<!-- @generated: do not edit by hand; use `agent-memory rebuild-index` -->"

// BuildIndexContent walks memDir and produces the index.md routing file
// per design §10.1. The output is DETERMINISTIC for a given tree — no
// wall-clock timestamps in the body — so regeneration only changes the
// file when the underlying memory actually changed, avoiding needless
// git churn and keeping tests stable.
//
// Sections produced:
//
//	# Agent Memory Index + @generated comment
//	## Always include   — local current state + conventions
//	## Topic map        — decisions (by status), pitfalls (count), modules
//	## Archive          — archived-context count
//	## Freshness        — placeholder until per-section freshness lands
func BuildIndexContent(memDir string, sch *schema.Schema) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Agent Memory Index\n")
	b.WriteString(indexGeneratedComment)
	b.WriteString("\n\n")

	// --- Always include ---
	b.WriteString("## Always include\n")
	b.WriteString("- local/current.<branch>.md — current local task state\n")
	if fileExists(memDir, "local/current.shared.md") {
		b.WriteString("- local/current.shared.md — cross-branch shared state\n")
	}
	if fileExists(memDir, "conventions.md") {
		b.WriteString("- conventions.md — build, test, style, workflow rules\n")
	}
	b.WriteString("\n")

	// --- Topic map ---
	b.WriteString("## Topic map\n")
	var topic []string

	if fileExists(memDir, "decisions.md") {
		summary, err := summariseDecisions(memDir)
		if err == nil {
			topic = append(topic, "- decisions.md — durable architecture/product decisions ("+summary+")")
		}
	}
	if fileExists(memDir, "pitfalls.md") {
		n, err := countAnchoredSections(memDir, "pitfalls.md")
		if err == nil {
			topic = append(topic, fmt.Sprintf("- pitfalls.md — known traps (%s)", pluralEntries(n)))
		}
	}
	for _, mod := range listModules(memDir) {
		topic = append(topic, fmt.Sprintf("- %s — module notes", mod))
	}

	if len(topic) == 0 {
		b.WriteString("(none yet — populated as memory grows)\n")
	} else {
		for _, line := range topic {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")

	// --- Archive ---
	archiveCount := countArchiveFiles(memDir)
	b.WriteString("## Archive\n")
	if archiveCount == 0 {
		b.WriteString("- archive/ — empty\n")
	} else {
		b.WriteString(fmt.Sprintf("- archive/ — %d archived context(s), fetched only on strong match\n", archiveCount))
	}
	b.WriteString("\n")

	// --- Freshness ---
	// Per-section freshness tracking (design §20.3) isn't implemented yet;
	// surface that honestly rather than print a misleading "all fresh".
	b.WriteString("## Freshness\n")
	b.WriteString("Per-section freshness tracking is not yet enabled; no stale areas are flagged.\n")

	return []byte(b.String()), nil
}

// RegenerateIndex rebuilds index.md and writes it atomically IF the new
// content differs from what's on disk. Returns whether the file changed.
//
// Best-effort by contract: callers in the apply path ignore the error
// (the durable bytes already landed; a stale index can be rebuilt via
// `agent-memory rebuild-index`). The "changed" return lets the apply
// path decide whether to include index.md in a git auto-stage batch.
func RegenerateIndex(memDir string, sch *schema.Schema) (bool, error) {
	content, err := BuildIndexContent(memDir, sch)
	if err != nil {
		return false, fmt.Errorf("RegenerateIndex: build: %w", err)
	}
	path := filepath.Join(memDir, indexFileName)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return false, nil // already current; no write, no churn
	}
	if err := agentfs.WriteAtomic(path, content, 0644); err != nil {
		return false, fmt.Errorf("RegenerateIndex: write: %w", err)
	}
	return true, nil
}

// =============================================================================
// helpers
// =============================================================================

func fileExists(memDir, rel string) bool {
	return agentfs.PathExists(filepath.Join(memDir, filepath.FromSlash(rel)))
}

// summariseDecisions tallies decision sections by their Status field,
// returning a string like "3 active, 2 superseded". Only nested
// (level-2+) anchored sections count; the top-level "# Decisions"
// heading is skipped. Sections with no/blank Status are counted as
// "unspecified".
func summariseDecisions(memDir string) (string, error) {
	src, err := os.ReadFile(filepath.Join(memDir, "decisions.md"))
	if err != nil {
		return "", err
	}
	sections, err := agentmd.ParseSections(src)
	if err != nil {
		return "", err
	}
	// Stable status order for deterministic output.
	order := []string{"active", "superseded", "deprecated", "proposed", "unspecified"}
	counts := map[string]int{}
	total := 0
	for _, sec := range sections {
		if sec.HeadingLevel < 2 || sec.AnchorID == "" {
			continue
		}
		total++
		bodyStart := findSectionBodyStart(src, sec.ByteStart)
		status := strings.ToLower(extractField(src[bodyStart:sec.ByteEnd], "Status"))
		switch status {
		case "active", "superseded", "deprecated", "proposed":
			counts[status]++
		default:
			counts["unspecified"]++
		}
	}
	if total == 0 {
		return "no entries yet", nil
	}
	var parts []string
	for _, k := range order {
		if counts[k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
		}
	}
	return strings.Join(parts, ", "), nil
}

// countAnchoredSections returns the number of nested (level-2+) anchored
// sections in the file at rel — i.e. the entry count, excluding the
// top-level document heading.
func countAnchoredSections(memDir, rel string) (int, error) {
	src, err := os.ReadFile(filepath.Join(memDir, filepath.FromSlash(rel)))
	if err != nil {
		return 0, err
	}
	sections, err := agentmd.ParseSections(src)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sec := range sections {
		if sec.HeadingLevel >= 2 && sec.AnchorID != "" {
			n++
		}
	}
	return n, nil
}

// listModules returns sorted forward-slash paths of modules/*.md files.
func listModules(memDir string) []string {
	dir := filepath.Join(memDir, "modules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var mods []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		mods = append(mods, "modules/"+e.Name())
	}
	sort.Strings(mods)
	return mods
}

// countArchiveFiles counts *.md files anywhere under archive/.
func countArchiveFiles(memDir string) int {
	dir := filepath.Join(memDir, "archive")
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // archive/ may not exist; treat as zero
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			n++
		}
		return nil
	})
	return n
}

// extractField scans a section body for a `Name:` field line and returns
// its value. Tolerates markdown emphasis around the label and value
// (`**Status:** active`, `*Status:* active`, plain `Status: active`),
// mirroring schema.parseFieldLines. Returns "" if not found.
func extractField(body []byte, name string) string {
	lower := strings.ToLower(name)
	for _, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Strip leading emphasis markers.
		work := strings.TrimLeft(line, "*")
		idx := strings.Index(work, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(strings.TrimRight(work[:idx], "*"))
		if strings.ToLower(key) != lower {
			continue
		}
		val := strings.TrimSpace(work[idx+1:])
		val = strings.TrimSpace(strings.Trim(val, "*"))
		return val
	}
	return ""
}

func pluralEntries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}
