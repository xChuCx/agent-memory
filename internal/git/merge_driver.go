package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// mergeDriverName is the git merge-driver key used in .gitattributes
// (`merge=agent-memory`) and in `.git/config` (`[merge "agent-memory"]`).
const mergeDriverName = "agent-memory"

// mergeDriverCommand is the driver git invokes for a 3-way merge of a
// matching file. git substitutes %O/%A/%B/%P with the ancestor, current
// (ours), other (theirs), and path; the driver writes the result to %A.
// Requires `agent-memory` on PATH.
const mergeDriverCommand = "agent-memory merge-driver %O %A %B %P"

// InstallMergeDriver registers the section-aware merge driver in the repo's
// LOCAL git config (`.git/config`). This is per-clone state and cannot be
// committed, so each teammate runs it once after cloning; the matching
// `.gitattributes` entry (which IS committed) is written by the CLI.
//
// Returns ErrGitNotInstalled if git is missing, or an error if root is not a
// git work tree.
func InstallMergeDriver(root string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return ErrGitNotInstalled
	}
	if !insideWorkTree(root) {
		return fmt.Errorf("InstallMergeDriver: %s is not a git work tree", root)
	}
	for _, kv := range [][2]string{
		{"merge." + mergeDriverName + ".name", "agent-memory section-aware merge"},
		{"merge." + mergeDriverName + ".driver", mergeDriverCommand},
	} {
		if _, err := runGit(root, "config", kv[0], kv[1]); err != nil {
			return fmt.Errorf("InstallMergeDriver: git config %s: %w", kv[0], err)
		}
	}
	return nil
}

// MergeDriverInstalled reports whether the merge driver is registered in the
// repo's local git config. Best-effort: any error (no git, not a repo) → false.
func MergeDriverInstalled(root string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	out, err := runGit(root, "config", "--get", "merge."+mergeDriverName+".driver")
	return err == nil && strings.TrimSpace(out) != ""
}
