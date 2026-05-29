package git

import (
	"fmt"
	"os/exec"
	"strings"
)

// ChangedFiles returns the repo-relative paths (forward-slash) of files with
// uncommitted changes in the working tree at root: modified, added, deleted,
// renamed, and untracked. It powers the "decisions/pitfalls referencing
// changed files" ranking signal (design §20.4) — sections that talk about a
// file you're currently touching should rank higher.
//
// Behaviour mirrors ActiveBranch's "not a repo is not an error" contract:
//   - root not inside a git work tree → (nil, nil).
//   - `git` binary missing → ErrGitNotInstalled.
//   - any other git failure is wrapped and returned.
//
// Paths are de-duplicated and returned in git's order. For renames
// (`R  old -> new`) only the new path is reported. Deleted files are
// included (a decision may reference a file you just removed).
func ChangedFiles(root string) ([]string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, ErrGitNotInstalled
	}
	// Probe for a work tree first so a non-repo root is a clean no-op
	// rather than a wrapped error.
	if out, err := runGit(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil, nil
	} else if strings.TrimSpace(out) != "true" {
		return nil, nil
	}

	// --porcelain v1: stable, script-friendly. -z avoids path-quoting
	// ambiguity (entries are NUL-separated; rename pairs are two NUL
	// fields). --untracked-files=all so newly created files count.
	out, err := runGit(root, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("ChangedFiles: %w", err)
	}
	return parsePorcelainZ(out), nil
}

// parsePorcelainZ decodes `git status --porcelain -z` output. Records are
// NUL-separated. Each record is "XY <path>"; a rename/copy (status starting
// R or C) is followed by a second NUL-separated field carrying the ORIGINAL
// path, which we skip — we report the destination.
func parsePorcelainZ(out string) []string {
	fields := strings.Split(out, "\x00")
	var paths []string
	seen := map[string]struct{}{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			// Trailing empty field after the final NUL, or malformed entry.
			continue
		}
		status := f[:2]
		path := f[3:] // skip "XY "
		// Rename/copy entries consume the next field (the original path).
		if status[0] == 'R' || status[0] == 'C' {
			i++ // skip the origin path field
		}
		add(path)
	}
	return paths
}
