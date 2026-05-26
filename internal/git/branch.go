// Package git exposes a minimal shell-out wrapper around the system `git`
// binary. The agent-memory project uses it for two reads:
//
//   - ActiveBranch resolves the current branch name (or short SHA, if HEAD
//     is detached) for branch-aware local-state file resolution per design
//     doc v0.4.1 §13.
//   - SlugBranch converts a branch name into the path-safe form used in
//     local/current.<slug>.md filenames.
//
// Writes (auto-stage, commit on apply) live in subsequent files in this
// package; M2 only ships the read path.
package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// BranchInfo describes the active branch of a repository.
type BranchInfo struct {
	// Name is the symbolic branch name (e.g., "main", "feature/auth").
	// Empty when IsDetached is true.
	Name string

	// ShortSHA is the abbreviated commit SHA, populated when IsDetached
	// is true so callers can build a stable "current.detached-<sha>" file.
	ShortSHA string

	// IsDetached is true when HEAD does not point to a branch ref.
	IsDetached bool

	// IsGitRepo is false when the root is not inside any git working tree.
	// In that case Name and ShortSHA are empty and no error is returned —
	// the agent-memory layout falls back to a single shared local file.
	IsGitRepo bool
}

// ErrGitNotInstalled is returned by ActiveBranch when the `git` executable
// cannot be found on PATH.
var ErrGitNotInstalled = errors.New("git: executable not found on PATH")

// ActiveBranch resolves the branch info for the working tree at root.
//
// Behaviour:
//   - root not a git repo → BranchInfo{IsGitRepo: false}, nil error.
//   - normal branch checked out → Name set, IsDetached false.
//   - detached HEAD → Name empty, ShortSHA set, IsDetached true.
//   - `git` binary missing → ErrGitNotInstalled.
//
// All other git errors (e.g., corrupted .git, permission issues) are
// wrapped and returned.
func ActiveBranch(root string) (BranchInfo, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return BranchInfo{}, ErrGitNotInstalled
	}

	// Probe whether root is inside a git work tree. Use -C so we don't
	// depend on the caller's cwd.
	if out, err := runGit(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		// Git exits non-zero outside a repo; treat that as "not a repo"
		// rather than an error.
		return BranchInfo{IsGitRepo: false}, nil
	} else if strings.TrimSpace(out) != "true" {
		return BranchInfo{IsGitRepo: false}, nil
	}

	name, err := runGit(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return BranchInfo{}, fmt.Errorf("ActiveBranch: %w", err)
	}
	name = strings.TrimSpace(name)

	if name == "HEAD" {
		// Detached HEAD — fetch the short SHA so callers can stably name
		// per-detached-state local files.
		sha, err := runGit(root, "rev-parse", "--short", "HEAD")
		if err != nil {
			return BranchInfo{}, fmt.Errorf("ActiveBranch: short SHA: %w", err)
		}
		return BranchInfo{
			ShortSHA:   strings.TrimSpace(sha),
			IsDetached: true,
			IsGitRepo:  true,
		}, nil
	}
	return BranchInfo{
		Name:      name,
		IsGitRepo: true,
	}, nil
}

// runGit runs `git -C root args...` and returns stdout. Stderr is discarded;
// failures surface via the wrapped error.
func runGit(root string, args ...string) (string, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", full...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SlugBranch converts a branch name to the path-safe slug used in
// local/current.<slug>.md filenames per design doc v0.4.1 §13.1.
//
// Rules:
//   - lowercase
//   - alphanumeric (a-z, 0-9) survive
//   - any other rune (including '/', spaces, punctuation) becomes a dash
//   - runs of dashes collapse to a single dash
//   - leading/trailing dashes stripped
//   - empty input → empty output
//
// Examples:
//   - "main"                  → "main"
//   - "feature/auth-rotation" → "feature-auth-rotation"
//   - "bugfix/JIRA-123"       → "bugfix-jira-123"
//   - "release/2026.05.01"    → "release-2026-05-01"
//
// This is intentionally aggressive: branch names with unicode or odd
// punctuation collapse to a stable ASCII form that's safe on every
// filesystem we care about.
func SlugBranch(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}
