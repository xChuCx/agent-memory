package memory

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/agent-memory/agent-memory/internal/config"
	agentgit "github.com/agent-memory/agent-memory/internal/git"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// MemoryStatus matches the design-doc §15.11 output shape for the
// `memory.status` MCP tool. Same type is used by the CLI `status`
// subcommand so both transports return identical structured data.
//
// Some fields are populated with conservative approximations until the
// underlying mechanism lands — they're documented inline.
type MemoryStatus struct {
	MemoryVersion     string             `json:"memory_version"`
	Repo              string             `json:"repo"`
	ActiveBranch      string             `json:"active_branch,omitempty"`
	DurableFiles      int                `json:"durable_files"`
	ArchiveFiles      int                `json:"archive_files"`
	LocalSessions     int                `json:"local_sessions"`
	LocalCurrentFiles int                `json:"local_current_files"`
	OrphanLocalFiles  []string           `json:"orphan_local_files,omitempty"`
	IndexSizeBytes    int64              `json:"index_size_bytes"`
	CurrentSizeBytes  int64              `json:"current_size_bytes"`
	StagedUpdates     []StagedStatusEntry `json:"staged_updates,omitempty"`

	// StaleNotes lists files flagged "stale" by future per-section
	// freshness tracking. Currently always empty (mechanism unimplemented
	// — see design doc §20.3).
	StaleNotes []string `json:"stale_notes,omitempty"`

	Security MemoryStatusSecurity `json:"security"`
	Git      MemoryStatusGit      `json:"git"`
	Lock     MemoryStatusLock     `json:"lock"`
}

// StagedStatusEntry is the per-proposal summary inside
// MemoryStatus.StagedUpdates.
type StagedStatusEntry struct {
	ID                  string   `json:"id"`
	Intent              string   `json:"intent"`
	AgeSeconds          int      `json:"age_seconds"`
	TTLRemainingSeconds int      `json:"ttl_remaining_seconds"`
	TargetFiles         []string `json:"target_files"`
	DriftDetected       bool     `json:"drift_detected"`
}

// MemoryStatusSecurity is the §15.11 `security` sub-block.
type MemoryStatusSecurity struct {
	// LastSecretScan is one of "passed" | "n/a" | "failed". Currently
	// always "n/a" — there's no per-write scan history persisted. Future
	// work (M8b2) will populate this from a scan log.
	LastSecretScan string `json:"last_secret_scan"`

	// AllowlistedRegions is the total count of secret-scan allowlist
	// regions discovered across all durable .md files at status time.
	AllowlistedRegions int `json:"allowlisted_regions"`

	// UntrustedSources counts proposals with sources of type
	// "external" or "inference" recorded against durable files. Not yet
	// persisted; always 0 until proposal history tracking lands.
	UntrustedSources int `json:"untrusted_sources"`
}

// MemoryStatusGit is the §15.11 `git` sub-block.
type MemoryStatusGit struct {
	TrackLocal           bool `json:"track_local"`
	TrackSessions        bool `json:"track_sessions"`
	IgnoredLocalState    bool `json:"ignored_local_state"`
	MergeDriverInstalled bool `json:"merge_driver_installed"`
}

// MemoryStatusLock is the §15.11 `lock` sub-block.
type MemoryStatusLock struct {
	Held bool `json:"held"`
	// StaleRecoveriesLast24h would count crash-recovered locks in a 24h
	// window. The kernel handles lock release on process death so this
	// is informational and currently always 0 until persisted.
	StaleRecoveriesLast24h int `json:"stale_recoveries_last_24h"`
}

// StatusDeps bundles the inputs BuildStatus needs. MemoryDir is the
// absolute path to .agent-memory/; Manifest + Schema are loaded; Branch
// is optional (zero value treated as "not in a git repo").
type StatusDeps struct {
	MemoryDir     string
	Manifest      *config.Manifest
	Schema        *schema.Schema
	Branch        agentgit.BranchInfo
	MemoryVersion string // "0.X.Y" or "dev"
}

// BuildStatus walks the .agent-memory/ tree and assembles a
// MemoryStatus matching design §15.11. Read-only; never modifies any
// file. Acquires the advisory lock briefly to determine `lock.held`
// (best-effort).
func BuildStatus(ctx context.Context, deps StatusDeps) (*MemoryStatus, error) {
	if deps.MemoryDir == "" {
		return nil, errors.New("BuildStatus: MemoryDir is required")
	}
	if deps.Manifest == nil || deps.Schema == nil {
		return nil, errors.New("BuildStatus: Manifest and Schema are required")
	}

	st := &MemoryStatus{
		MemoryVersion: deps.MemoryVersion,
		Repo:          deps.Manifest.Project.Name,
		ActiveBranch:  deps.Branch.Name,
		Security: MemoryStatusSecurity{
			LastSecretScan:    "n/a",
			AllowlistedRegions: 0,
			UntrustedSources:  0,
		},
		Git: MemoryStatusGit{
			TrackLocal:           deps.Manifest.Git.TrackLocal,
			TrackSessions:        deps.Manifest.Git.TrackSessions,
			MergeDriverInstalled: deps.Manifest.Git.MergeDriverInstalled,
		},
	}

	if err := countFiles(deps.MemoryDir, deps.Schema, st); err != nil {
		return nil, fmt.Errorf("BuildStatus: count files: %w", err)
	}
	if err := computeSizes(deps.MemoryDir, deps.Branch, st); err != nil {
		return nil, fmt.Errorf("BuildStatus: sizes: %w", err)
	}
	if err := findOrphanLocalFiles(deps.MemoryDir, deps.Branch, st); err != nil {
		return nil, fmt.Errorf("BuildStatus: orphans: %w", err)
	}
	if err := loadStagedSummaries(deps, st); err != nil {
		return nil, fmt.Errorf("BuildStatus: staged: %w", err)
	}
	if err := scanAllowlistTotals(deps.MemoryDir, st); err != nil {
		// Non-fatal; just leave the count at zero.
		st.Security.AllowlistedRegions = 0
	}
	st.Git.IgnoredLocalState = checkGitignoresLocal(deps.MemoryDir)
	st.Lock.Held = lockIsHeld(deps.MemoryDir)

	return st, nil
}

// countFiles walks memDir and tallies durable/archive/sessions/current
// file counts. Files matching no schema category are silently ignored.
func countFiles(memDir string, sch *schema.Schema, st *MemoryStatus) error {
	return filepath.WalkDir(memDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		rel, _ := filepath.Rel(memDir, p)
		relSlash := filepath.ToSlash(rel)

		switch {
		case strings.HasPrefix(relSlash, "archive/"):
			st.ArchiveFiles++
		case strings.HasPrefix(relSlash, "sessions/"):
			st.LocalSessions++
		case strings.HasPrefix(relSlash, "local/"):
			st.LocalCurrentFiles++
		default:
			// "durable" = matches a category that is git-tracked and
			// not under local/sessions/archive — i.e., the canonical
			// long-lived memory files.
			cat, ok := sch.CategoryForPath(relSlash)
			if !ok {
				return nil
			}
			if cat.GitTracked && !cat.WriteOnce && cat.Name != "index" {
				st.DurableFiles++
			} else if cat.Name == "index" {
				st.DurableFiles++ // index.md counts; it's not write-once but server-managed
			}
		}
		return nil
	})
}

// computeSizes stats the index DB + the active branch's local current
// file + the shared current file, summing the two current files into
// CurrentSizeBytes.
func computeSizes(memDir string, branch agentgit.BranchInfo, st *MemoryStatus) error {
	if info, err := os.Stat(filepath.Join(memDir, "meta", "index.sqlite")); err == nil {
		st.IndexSizeBytes = info.Size()
	}
	// Sum current.<branch>.md + current.shared.md sizes.
	sharedPath := filepath.Join(memDir, "local", "current.shared.md")
	if info, err := os.Stat(sharedPath); err == nil {
		st.CurrentSizeBytes += info.Size()
	}
	if branch.IsGitRepo && !branch.IsDetached && branch.Name != "" {
		slug := agentgit.SlugBranch(branch.Name)
		if slug != "" {
			branchPath := filepath.Join(memDir, "local", "current."+slug+".md")
			if info, err := os.Stat(branchPath); err == nil {
				st.CurrentSizeBytes += info.Size()
			}
		}
	}
	return nil
}

// findOrphanLocalFiles enumerates local/current.<slug>.md files and
// flags those whose slug doesn't correspond to any currently-existing
// branch. Outside a git repo, returns no orphans (every local file
// looks "valid" because we can't list branches to compare against).
func findOrphanLocalFiles(memDir string, branch agentgit.BranchInfo, st *MemoryStatus) error {
	if !branch.IsGitRepo {
		return nil
	}
	localDir := filepath.Join(memDir, "local")
	entries, err := os.ReadDir(localDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	// Gather branch slugs that currently exist (best-effort — if `git
	// branch` fails we leave orphans empty rather than misreport).
	validSlugs, err := branchSlugs(memDir, branch)
	if err != nil {
		return nil
	}

	var orphans []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "current.") || !strings.HasSuffix(name, ".md") {
			continue
		}
		slug := strings.TrimSuffix(strings.TrimPrefix(name, "current."), ".md")
		if slug == "shared" {
			continue // current.shared.md is intentional
		}
		if strings.HasPrefix(slug, "detached-") {
			// detached-HEAD files reference SHAs; out of scope for the
			// "branch exists?" check.
			continue
		}
		if _, ok := validSlugs[slug]; !ok {
			orphans = append(orphans, filepath.ToSlash(filepath.Join("local", name)))
		}
	}
	sort.Strings(orphans)
	st.OrphanLocalFiles = orphans
	return nil
}

// branchSlugs returns the set of currently-existing branch slugs.
// memDir's parent IS the repo root by agent-memory convention.
func branchSlugs(memDir string, branch agentgit.BranchInfo) (map[string]struct{}, error) {
	// We use a tiny shell-out to git here. The git package's ActiveBranch
	// has the wrapper but not a list-branches helper. Reuse the exec
	// pattern locally without introducing a public API in git/ for one
	// caller.
	if !branch.IsGitRepo {
		return nil, errors.New("not a git repo")
	}
	// Walk back from memDir to find repo root: by convention memDir =
	// <root>/.agent-memory, so root = filepath.Dir(memDir).
	root := filepath.Dir(memDir)
	out, err := agentgit.ListLocalBranches(root)
	if err != nil {
		return nil, err
	}
	slugs := make(map[string]struct{}, len(out))
	for _, b := range out {
		s := agentgit.SlugBranch(b)
		if s != "" {
			slugs[s] = struct{}{}
		}
	}
	return slugs, nil
}

// loadStagedSummaries reads staging/ and builds StagedUpdates entries.
// drift_detected is computed by running CheckDrift against current
// disk state for each staged target — same machinery `apply` uses.
func loadStagedSummaries(deps StatusDeps, st *MemoryStatus) error {
	proposals, err := ListStaged(deps.MemoryDir)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ttl := time.Duration(deps.Manifest.Staging.TTLSeconds) * time.Second

	for _, p := range proposals {
		entry := StagedStatusEntry{
			ID:          p.StagingID,
			Intent:      string(p.Request.Intent),
			TargetFiles: append([]string(nil), p.Files...),
		}
		// Age and TTL remaining.
		if stagedAt, err := time.Parse(time.RFC3339, p.StagedAt); err == nil {
			age := now.Sub(stagedAt)
			entry.AgeSeconds = int(age.Seconds())
			if ttl > 0 {
				remaining := ttl - age
				if remaining < 0 {
					remaining = 0
				}
				entry.TTLRemainingSeconds = int(remaining.Seconds())
			}
		}
		// Drift: any target reports drift → entry.DriftDetected = true.
		if targets, err := LoadStagedTargets(deps.MemoryDir, p.StagingID); err == nil {
			for _, t := range targets {
				report, cerr := CheckDrift(deps.MemoryDir, t)
				if cerr != nil || report != nil {
					entry.DriftDetected = true
					break
				}
			}
		}
		st.StagedUpdates = append(st.StagedUpdates, entry)
	}
	return nil
}

// scanAllowlistTotals walks all .md files under memDir and totals the
// number of secret-scan allowlist regions. Files that fail to parse
// allowlist syntax don't break the count — they're treated as having
// zero regions (the orchestrator would reject any propose on them).
func scanAllowlistTotals(memDir string, st *MemoryStatus) error {
	return filepath.WalkDir(memDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		regions, err := ExtractAllowlistRegions(body)
		if err != nil {
			return nil // skip malformed
		}
		st.Security.AllowlistedRegions += len(regions)
		return nil
	})
}

// checkGitignoresLocal returns true if the .agent-memory/.gitignore
// excludes `local/` (the design's recommended default). Surface in
// status so users can tell whether their branch-local files are
// keeping out of git history.
func checkGitignoresLocal(memDir string) bool {
	body, err := os.ReadFile(filepath.Join(memDir, ".gitignore"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "local/" || line == "local" {
			return true
		}
	}
	return false
}

// lockIsHeld probes the advisory lock with a non-blocking TryLock.
// If TryLock returns false, someone else holds it. If true, we own it
// transiently; release immediately and return "not held". Best-effort
// (a status read is informational, racing other processes is fine).
func lockIsHeld(memDir string) bool {
	lockPath := filepath.Join(memDir, "meta", "lock")
	if _, err := os.Stat(lockPath); err != nil {
		return false
	}
	fl := flock.New(lockPath)
	ok, err := fl.TryLock()
	if err != nil {
		return false
	}
	if !ok {
		return true
	}
	_ = fl.Unlock()
	return false
}
