package memory

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xChuCx/agent-memory/internal/config"
	agentgit "github.com/xChuCx/agent-memory/internal/git"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// AutoStageResult reports what auto-stage did. Returned through the
// apply paths (Direct apply and ApplyStaged) so the CLI can surface it
// in its human + JSON output. Empty / zero-valued when auto-stage is
// disabled or the project isn't a git repo.
type AutoStageResult struct {
	// Staged is the list of repo-root-relative paths git add was called
	// with. Forward slashes.
	Staged []string `json:"staged,omitempty"`

	// CommitSHA is non-empty when auto_commit was on AND a commit was
	// actually created (nothing staged ≠ commit).
	CommitSHA string `json:"commit_sha,omitempty"`

	// Errors, if any. Auto-stage NEVER fails an apply — errors are
	// collected for surfacing only. Bytes are durable; git is a
	// best-effort side channel.
	Errors []string `json:"errors,omitempty"`

	// Skipped is true when auto-stage logic ran but produced no work
	// (manifest disabled, not-a-repo, nothing tracked). Distinguishes
	// "ran and did nothing" from "didn't run".
	Skipped bool `json:"skipped,omitempty"`
}

// shouldStage reports whether the file at memRel (forward-slash,
// .agent-memory/-rooted) should be passed to `git add` under the given
// schema + git manifest config.
//
// Decision (in order):
//
//  1. If the file falls into a known schema category AND that
//     category's GitTracked is true → stage.
//  2. If the file lives under local/  AND manifest.git.track_local is
//     true → stage (override the default-untracked behaviour).
//  3. If the file lives under sessions/ AND manifest.git.track_sessions
//     is true → stage.
//  4. Otherwise → skip.
//
// Files outside any category (e.g., user-dropped notes in a folder
// agent-memory doesn't know about) are always skipped — auto-stage is
// conservative.
func shouldStage(memRel string, sch *schema.Schema, gitCfg config.Git) bool {
	if sch == nil {
		return false
	}
	memRel = filepath.ToSlash(memRel)
	if cat, ok := sch.CategoryForPath(memRel); ok && cat.GitTracked {
		return true
	}
	if strings.HasPrefix(memRel, "local/") && gitCfg.TrackLocal {
		return true
	}
	if strings.HasPrefix(memRel, "sessions/") && gitCfg.TrackSessions {
		return true
	}
	return false
}

// maybeAutoStage runs the git auto-stage + auto-commit side effects for
// a successful apply. It is feature-gated on manifest.git.auto_stage_changes:
// when false (the default in DefaultManifest), the function returns
// AutoStageResult{Skipped: true} without touching git at all.
//
// Contract:
//   - Files outside the staged set (manifest + schema policy) are
//     silently dropped.
//   - git failures are appended to Errors; they NEVER cause the apply
//     to fail. The orchestrator's bytes are already on disk via
//     WriteAtomic; git is downstream.
//   - When auto_commit is false, AddPaths runs but Commit doesn't —
//     leaves the staged changes for the user's next `git commit`.
//   - Commit message format:
//       <prefix> <intent> — <rationale-or-empty>
//       <blank line>
//       Files: <comma-separated relative paths>
//
//     Title length is bounded by the rationale length; we don't truncate
//     defensively. If a user wants a tighter ceiling they can post-
//     process by setting auto_commit: false and committing themselves.
//
// memDir is the absolute path to .agent-memory/ (used to resolve the
// repo root and rebase the per-file paths). memRelFiles are
// memDir-rooted forward-slash paths.
func maybeAutoStage(
	deps UpdateDeps,
	repoRoot string,
	memRelFiles []string,
	intent Intent,
	rationale string,
) AutoStageResult {
	if deps.Manifest == nil || !deps.Manifest.Git.AutoStageChanges {
		return AutoStageResult{Skipped: true}
	}

	var (
		toStage []string // git-add args, repo-root-relative forward-slash
		result  AutoStageResult
	)
	memDirRel, err := filepath.Rel(repoRoot, deps.MemoryDir)
	if err != nil {
		result.Errors = append(result.Errors,
			fmt.Sprintf("autostage: rel(%q, %q): %v", repoRoot, deps.MemoryDir, err))
		return result
	}
	memDirRel = filepath.ToSlash(memDirRel)

	for _, mf := range memRelFiles {
		if !shouldStage(mf, deps.Schema, deps.Manifest.Git) {
			continue
		}
		toStage = append(toStage, memDirRel+"/"+mf)
	}
	if len(toStage) == 0 {
		result.Skipped = true
		return result
	}

	staged, err := agentgit.AddPaths(repoRoot, toStage)
	if err != nil {
		if errors.Is(err, agentgit.ErrGitNotInstalled) {
			// Not an error: a project might genuinely lack git. Mark as
			// skipped so the CLI can render "no git" rather than failure.
			result.Skipped = true
			return result
		}
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.Staged = staged

	if deps.Manifest.Git.AutoCommit {
		msg := composeCommitMessage(deps.Manifest.Git.CommitMessagePrefix, intent, rationale, staged)
		sha, err := agentgit.Commit(repoRoot, msg)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		}
		result.CommitSHA = sha
	}
	return result
}

// composeCommitMessage builds the commit message. Title is on the first
// line; an optional body follows the blank line.
func composeCommitMessage(prefix string, intent Intent, rationale string, staged []string) string {
	if prefix == "" {
		prefix = "chore(memory):"
	}
	var title string
	if rationale != "" {
		title = fmt.Sprintf("%s %s — %s", prefix, intent, rationale)
	} else {
		title = fmt.Sprintf("%s %s", prefix, intent)
	}
	if len(staged) == 0 {
		return title
	}
	return title + "\n\nFiles: " + strings.Join(staged, ", ") + "\n"
}
