// Package memory implements the agent-facing operations: BuildContextPack
// (M2 T2.7), and — added later — the structured update pipeline (M3) and
// staging engine (M5).
//
// fetch.go assembles the Markdown context pack that memory.fetch_context
// returns. The algorithm follows design doc v0.4.1 §20.5:
//
//   1. Empty query → bootstrap pack (current.<branch>.md + current.shared.md
//      + conventions.md + a compact index.md summary).
//   2. Non-empty query → Index.Search + ranking + section-level assembly,
//      with the bootstrap files always prepended.
//
// Budget enforcement is greedy: sections are appended in ranked order
// until the running character total would exceed Budget, then the
// remaining sections move to the Omitted list.
package memory

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/index"
	agentmd "github.com/xChuCx/agent-memory/internal/markdown"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// FetchRequest mirrors the memory.fetch_context MCP tool input.
type FetchRequest struct {
	Query          string
	Scope          []string
	Budget         int      // characters; 0 = use manifest default
	Include        []string // categories (advisory in M2; M3 enforces)
	ExcludeArchive bool     // hard exclude vs penalize
}

// FetchResponse is the structured shape returned to the caller. The MCP
// tool serialises this verbatim; the CLI emits the Context field by default
// and the whole structure under --json.
type FetchResponse struct {
	Context              string          `json:"context"`
	IncludedFiles        []IncludedFile  `json:"included_files"`
	Omitted              []OmittedFile   `json:"omitted,omitempty"`
	SuggestedNextQueries []string        `json:"suggested_next_queries,omitempty"`
	ContextMetadata      ContextMetadata `json:"context_metadata"`
}

// IncludedFile describes one file (possibly contributing multiple sections)
// that ended up in the pack.
type IncludedFile struct {
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	Freshness    string `json:"freshness,omitempty"`
	Confidence   string `json:"confidence,omitempty"`
	SectionCount int    `json:"section_count,omitempty"`
}

// OmittedFile describes a candidate that was dropped (budget exceeded or
// relevance below threshold).
type OmittedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ContextMetadata is sidecar info the agent can use to decide follow-up
// behaviour without re-fetching.
type ContextMetadata struct {
	ActiveBranch    string   `json:"active_branch,omitempty"`
	BudgetUsed      int      `json:"budget_used"`
	BudgetRemaining int      `json:"budget_remaining"`
	StaleWarnings   []string `json:"stale_warnings,omitempty"`
}

// FetchDeps bundles the dependencies BuildContextPack needs. Callers
// (CLI fetch, MCP server handler) construct this and reuse it for the
// lifetime of one request.
type FetchDeps struct {
	Idx       *index.Index
	Schema    *schema.Schema
	Manifest  *config.Manifest
	MemoryDir string // absolute path to .agent-memory/
	Branch    git.BranchInfo

	// ChangedFiles are repo-relative paths with uncommitted changes,
	// resolved by the caller (CLI / MCP) via git.ChangedFiles. Feeds the
	// "decisions/pitfalls referencing changed files" ranking signal. Empty
	// is fine (no boost) — outside a git repo, or on a clean tree.
	ChangedFiles []string

	// Logger is optional; nil → discard. See UpdateDeps.log().
	Logger *slog.Logger
}

func (d FetchDeps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return nopLogger
}

// BuildContextPack is the entry point. It dispatches between the bootstrap
// path (empty query) and the search path (non-empty query). All output goes
// into FetchResponse.
func BuildContextPack(ctx context.Context, req FetchRequest, deps FetchDeps) (resp *FetchResponse, err error) {
	budget := req.Budget
	if budget <= 0 {
		budget = deps.Manifest.Budgets.FetchContextChars
	}
	if budget <= 0 {
		budget = 24000 // hard fallback if manifest is missing the field
	}

	mode := "bootstrap"
	if strings.TrimSpace(req.Query) != "" {
		mode = "search"
	}
	log := deps.log()
	defer func() {
		if err != nil || resp == nil {
			return
		}
		log.Debug("fetch_context served",
			"mode", mode,
			"included_files", len(resp.IncludedFiles),
			"omitted", len(resp.Omitted),
			"budget_used", resp.ContextMetadata.BudgetUsed,
			"budget", budget)
	}()

	if mode == "bootstrap" {
		return buildBootstrapPack(ctx, deps, budget)
	}
	return buildSearchPack(ctx, req, deps, budget)
}

// buildBootstrapPack returns the "no query" pack: branch-local current,
// shared current, conventions, and a compact index summary. Empty files
// are silently omitted.
func buildBootstrapPack(ctx context.Context, deps FetchDeps, budget int) (*FetchResponse, error) {
	branchLocal := branchLocalPath(deps.Branch)
	files := []struct {
		path   string
		reason string
	}{
		{branchLocal, "bootstrap: active branch local state"},
		{"local/current.shared.md", "bootstrap: cross-branch shared state"},
		{"conventions.md", "bootstrap: project conventions"},
		{"index.md", "bootstrap: memory index summary"},
	}

	var (
		pack     bytes.Buffer
		included []IncludedFile
		omitted  []OmittedFile
	)
	used := 0

	for _, f := range files {
		body, err := readMemoryFile(deps.MemoryDir, f.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue // missing bootstrap file is fine (e.g., no branch local yet)
			}
			return nil, fmt.Errorf("bootstrap read %s: %w", f.path, err)
		}
		if len(body) == 0 {
			continue
		}
		header := fmt.Sprintf("\n<!-- @file: %s -->\n", f.path)
		chunk := header + string(body)
		if used+len(chunk) > budget {
			omitted = append(omitted, OmittedFile{Path: f.path, Reason: "budget exhausted"})
			continue
		}
		pack.WriteString(chunk)
		used += len(chunk)
		included = append(included, IncludedFile{
			Path:         f.path,
			Reason:       f.reason,
			SectionCount: 1, // whole-file include
		})
	}

	return &FetchResponse{
		Context:       pack.String(),
		IncludedFiles: included,
		Omitted:       omitted,
		ContextMetadata: ContextMetadata{
			ActiveBranch:    deps.Branch.Name,
			BudgetUsed:      used,
			BudgetRemaining: budget - used,
		},
	}, nil
}

// buildSearchPack runs the FTS5 search, applies ranking, and assembles a
// section-level pack. The branch-local and shared files are always
// prepended (when present) before search results.
func buildSearchPack(ctx context.Context, req FetchRequest, deps FetchDeps, budget int) (*FetchResponse, error) {
	results, err := deps.Idx.Search(ctx, req.Query, 50)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	results = index.ApplyRankingSignals(results, index.RankingContext{
		Scope:        req.Scope,
		ActiveBranch: deps.Branch.Name,
		ChangedFiles: deps.ChangedFiles,
		// FreshFiles / StaleFiles wired once freshness markers are
		// persisted (design §20.3); for now these stay empty.
	})

	// Optional hard exclusion of archive.
	if req.ExcludeArchive {
		filtered := results[:0]
		for _, r := range results {
			if strings.HasPrefix(r.File, "archive/") {
				continue
			}
			filtered = append(filtered, r)
		}
		results = filtered
	}

	var (
		pack     bytes.Buffer
		included []IncludedFile
		omitted  []OmittedFile
		used     int
	)

	// Always prepend branch-local + shared current state if they exist.
	for _, p := range []string{branchLocalPath(deps.Branch), "local/current.shared.md"} {
		body, err := readMemoryFile(deps.MemoryDir, p)
		if err != nil || len(body) == 0 {
			continue
		}
		header := fmt.Sprintf("\n<!-- @file: %s -->\n", p)
		chunk := header + string(body)
		if used+len(chunk) > budget {
			omitted = append(omitted, OmittedFile{Path: p, Reason: "budget exhausted"})
			continue
		}
		pack.WriteString(chunk)
		used += len(chunk)
		included = append(included, IncludedFile{
			Path:         p,
			Reason:       "always-included local state",
			SectionCount: 1,
		})
	}

	// Cache parsed sections per file to avoid re-parsing when multiple
	// results come from the same file.
	type fileCache struct {
		src      []byte
		sections []agentmd.Section
	}
	cache := map[string]fileCache{}
	sectionCount := map[string]int{}

	// Token sets of sections already written into the pack, in rank order.
	// Used to drop near-duplicates (design §20.5 step 6): because results
	// arrive best-first, the first member of a duplicate cluster we accept
	// is the higher-ranked one, so a later match against it is the lower
	// rank → drop it.
	var acceptedTokens []map[string]struct{}

	for _, r := range results {
		fc, ok := cache[r.File]
		if !ok {
			src, err := readMemoryFile(deps.MemoryDir, r.File)
			if err != nil {
				omitted = append(omitted, OmittedFile{Path: r.File, Reason: "read error"})
				continue
			}
			sections, err := agentmd.ParseSections(src)
			if err != nil {
				omitted = append(omitted, OmittedFile{Path: r.File, Reason: "parse error"})
				continue
			}
			fc = fileCache{src: src, sections: sections}
			cache[r.File] = fc
		}
		sec, ok := agentmd.FindByID(fc.sections, r.SectionID)
		if !ok || sec == nil {
			// SectionID not present in the current file — index is stale
			// for this section. Skip; M3's incremental update path keeps
			// the index in sync.
			omitted = append(omitted, OmittedFile{Path: r.File, Reason: "section not in current file"})
			continue
		}
		body := fc.src[sec.ByteStart:sec.ByteEnd]

		// Semantic dedup BEFORE budget (design §20.5 step 6): a section that
		// merely repeats a higher-ranked one is dropped so it can't crowd
		// out distinct content. Tokenize the section body only — not the
		// synthetic @file header, whose per-section score would pollute the
		// token set.
		tokens := tokenize(string(body))
		if isNearDuplicate(tokens, acceptedTokens) {
			omitted = append(omitted, OmittedFile{Path: r.File, Reason: "near-duplicate of higher-ranked section"})
			continue
		}

		header := fmt.Sprintf("\n<!-- @file: %s @id: %s score: %.4f -->\n", r.File, r.SectionID, r.Score)
		chunk := header + string(body)
		if used+len(chunk) > budget {
			omitted = append(omitted, OmittedFile{Path: r.File, Reason: "budget exhausted"})
			continue
		}
		pack.WriteString(chunk)
		used += len(chunk)
		acceptedTokens = append(acceptedTokens, tokens)
		sectionCount[r.File]++
	}

	// Roll up included files (one IncludedFile per unique file with section count).
	for file, count := range sectionCount {
		included = append(included, IncludedFile{
			Path:         file,
			Reason:       "matched query",
			SectionCount: count,
		})
	}

	return &FetchResponse{
		Context:       pack.String(),
		IncludedFiles: included,
		Omitted:       omitted,
		ContextMetadata: ContextMetadata{
			ActiveBranch:    deps.Branch.Name,
			BudgetUsed:      used,
			BudgetRemaining: budget - used,
		},
	}, nil
}

// branchLocalPath returns the per-branch local-state file path under
// .agent-memory/, in forward-slash form. Falls back to
// "local/current.shared.md" when we are not in a git repo or on a
// detached HEAD (which doesn't have a stable branch name).
func branchLocalPath(b git.BranchInfo) string {
	if !b.IsGitRepo {
		return "local/current.shared.md"
	}
	if b.IsDetached {
		// Could use "local/current.detached-<sha>.md" but that file
		// is unlikely to exist; fall back to shared.
		return "local/current.shared.md"
	}
	slug := git.SlugBranch(b.Name)
	if slug == "" {
		return "local/current.shared.md"
	}
	return "local/current." + slug + ".md"
}

// readMemoryFile reads a forward-slash relative path from memDir.
func readMemoryFile(memDir, relSlash string) ([]byte, error) {
	full := filepath.Join(memDir, filepath.FromSlash(relSlash))
	return os.ReadFile(full)
}
