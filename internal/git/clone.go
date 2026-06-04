package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Clone runs `git clone <source> <dest>`. source may be a remote URL or a
// local path (git clones local paths without a network); dest must not already
// exist. ctx cancellation kills a hung clone/fetch. No shallow flags — checking
// out an arbitrary pinned commit later needs full history, and landscape stores
// are small.
func Clone(ctx context.Context, source, dest string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return ErrGitNotInstalled
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--", source, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %q: %w (output: %s)", source, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Checkout detaches HEAD at revision (a branch, tag, or commit SHA) in dir.
// Detaching means any ref kind is handled uniformly.
func Checkout(ctx context.Context, dir, revision string) error {
	if _, err := runGitCtx(ctx, dir, "checkout", "--detach", "--quiet", revision); err != nil {
		return fmt.Errorf("git checkout %q: %w", revision, err)
	}
	return nil
}

// HeadCommit returns the full commit SHA at HEAD in dir.
func HeadCommit(ctx context.Context, dir string) (string, error) {
	out, err := runGitCtx(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// IsWorkTree reports whether dir is inside a git work tree. Used by sync to
// decide whether a local-path store can be pinned to a commit.
func IsWorkTree(dir string) bool { return insideWorkTree(dir) }

// runGitCtx is the context-aware sibling of runGit (branch.go), so sync's git
// operations honor cancellation.
func runGitCtx(ctx context.Context, dir string, args ...string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", ErrGitNotInstalled
	}
	full := append([]string{"-C", dir}, args...)
	out, err := exec.CommandContext(ctx, "git", full...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
