package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// AddPaths runs `git add -- <paths...>` under root. Paths are repo-root-
// relative (forward or OS separator, git accepts both).
//
// Semantics:
//   - root is not a git work tree → returns (nil, nil). Auto-stage on a
//     non-git project is intentionally a no-op, not an error.
//   - git binary missing → returns ErrGitNotInstalled.
//   - Empty paths slice → returns (nil, nil).
//   - Real git failure (corrupt repo, locked index, etc.) → wrapped error.
//
// The returned slice is the input paths verbatim on success; future
// extensions might filter to actually-staged paths via `--dry-run` first,
// but the v0.1 contract is "tell git to add these; trust git's behaviour".
//
// Auto-stage NEVER calls `git add .` or any pattern broader than the
// explicit list. We stage only files our own pipeline just wrote.
func AddPaths(root string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrGitNotInstalled
	}
	if !insideWorkTree(root) {
		return nil, nil
	}

	args := append([]string{"-C", root, "add", "--"}, paths...)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git add: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return append([]string(nil), paths...), nil
}

// Commit creates a new commit with message under root and returns the
// commit SHA. Behaviour:
//
//   - root is not a git work tree → ("", nil). Symmetric with AddPaths.
//   - git binary missing → ErrGitNotInstalled.
//   - Nothing staged (`git diff --cached` is empty) → ("", nil). NOT an
//     error — a no-op apply (idempotent re-apply with no on-disk change)
//     should not produce empty commits.
//   - Real git failure (hook rejected, no user.email configured, etc.)
//     → wrapped error including stderr.
//
// Commit does NOT pass `--no-verify`. If the project has commit hooks,
// they run. Document for users: a slow pre-commit hook makes every apply
// slow; toggle `manifest.git.auto_commit: false` to opt out.
//
// The message is passed verbatim to `git commit -m`. Multi-line messages
// (a title followed by a blank line and a body) are supported.
func Commit(root, message string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", ErrGitNotInstalled
	}
	if !insideWorkTree(root) {
		return "", nil
	}

	if !hasStagedChanges(root) {
		return "", nil
	}

	cmd := exec.Command("git", "-C", root, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// Fetch the new HEAD SHA for the caller (informational; logged, used
	// in CLI output, etc.). Failure here is non-fatal — the commit
	// already exists.
	out, err := runGit(root, "rev-parse", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

// insideWorkTree reports whether root is inside a git working tree.
// Wraps the same probe ActiveBranch uses but exposes a boolean so
// callers can decide silently rather than handle an error.
func insideWorkTree(root string) bool {
	out, err := runGit(root, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// hasStagedChanges reports whether `git diff --cached --quiet` exits
// non-zero (meaning there ARE staged changes — that's git's convention).
//
// Used as a pre-commit guard so Commit doesn't error on "nothing to
// commit, working tree clean" — that's a normal state for an
// auto-stage path triggered by a no-op apply.
func hasStagedChanges(root string) bool {
	cmd := exec.Command("git", "-C", root, "diff", "--cached", "--quiet")
	err := cmd.Run()
	if err == nil {
		return false // exit 0 → no staged changes
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() == 1 // 1 → changes; >1 → real error (treat as no-op)
	}
	return false
}
