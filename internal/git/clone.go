package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// Clone runs `git clone <source> <dest>`. source may be a remote URL or a
// local path (git clones local paths without a network); dest must not already
// exist. No shallow/network-specific flags — checking out an arbitrary commit
// later needs full history, and landscape stores are small.
func Clone(source, dest string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return ErrGitNotInstalled
	}
	cmd := exec.Command("git", "clone", "--quiet", "--", source, dest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone %q: %w (output: %s)", source, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Checkout detaches HEAD at revision (a branch, tag, or commit SHA) in dir.
// Detaching means any ref kind is handled uniformly and we never leave a
// tracking branch behind in the throwaway clone.
func Checkout(dir, revision string) error {
	if _, err := runGit(dir, "checkout", "--detach", "--quiet", revision); err != nil {
		return fmt.Errorf("git checkout %q: %w", revision, err)
	}
	return nil
}

// HeadCommit returns the full commit SHA at HEAD in dir.
func HeadCommit(dir string) (string, error) {
	out, err := runGit(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// IsWorkTree reports whether dir is inside a git work tree. Used by sync to
// decide whether a local-path store can be pinned to a commit or must be
// recorded as unlocked.
func IsWorkTree(dir string) bool { return insideWorkTree(dir) }
