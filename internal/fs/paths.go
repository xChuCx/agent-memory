package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateMemoryPath validates a relative path intended to reference content
// inside the .agent-memory/ directory of a repository.
//
//   - root must be the absolute path to .agent-memory/.
//   - rel is a path relative to root. It may use either forward slashes or
//     the OS path separator; the result uses the OS separator.
//
// On success, returns the cleaned absolute path inside root.
//
// Rejects:
//   - non-absolute root (programmer error).
//   - absolute rel paths.
//   - rel paths containing ".." segments (including those that Clean
//     resolves to leaving rel above root).
//   - rel paths that target server-managed derived files (see IsDerivedPath).
//
// Symlink resolution is intentionally not performed here; callers that
// dereference symlinks should re-validate the resolved path before any
// filesystem access.
func ValidateMemoryPath(root, rel string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("ValidateMemoryPath: root must be absolute: %q", root)
	}
	if rel == "" {
		return "", fmt.Errorf("ValidateMemoryPath: rel must not be empty")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("ValidateMemoryPath: rel must be relative: %q", rel)
	}

	// Normalize to OS separators for Clean to do its job.
	relOS := filepath.FromSlash(rel)
	cleaned := filepath.Clean(relOS)

	// After Clean, ".." or starting with ".."+separator means rel escapes root.
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ValidateMemoryPath: path escapes root: %q", rel)
	}

	// Belt-and-suspenders: reject any path that still contains ".." after Clean.
	// Clean should have collapsed them, but a defensive check catches edge
	// cases on platforms with unusual separator handling.
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return "", fmt.Errorf("ValidateMemoryPath: path contains '..': %q", rel)
		}
	}

	// Check against the derived-paths refusal list. IsDerivedPath uses forward
	// slashes for portable matching.
	if IsDerivedPath(filepath.ToSlash(cleaned)) {
		return "", fmt.Errorf("ValidateMemoryPath: refusing derived path: %q", rel)
	}

	target := filepath.Join(root, cleaned)
	if err := checkSymlinkContainment(root, target); err != nil {
		return "", err
	}

	return target, nil
}

// checkSymlinkContainment verifies that any existing path component between root
// and target does not resolve outside root via symlinks.
//
// Security Notice: checkSymlinkContainment validates pre-existing filesystem structure
// against symlink traversal. It does not provide race-free TOCTOU kernel guarantees
// against concurrent directory replacement during write (e.g. openat2/RESOLVE_NO_SYMLINKS);
// for cooperative local agent operations this prevents static and pre-existing path escapes.
func checkSymlinkContainment(root, target string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			realRoot = filepath.Clean(root)
		} else {
			return fmt.Errorf("checkSymlinkContainment: eval root %s: %w", root, err)
		}
	} else {
		realRoot = filepath.Clean(realRoot)
	}

	// Find the longest existing prefix of target.
	curr := target
	for {
		if _, err := os.Lstat(curr); err == nil {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	resolvedTarget, err := filepath.EvalSymlinks(curr)
	if err != nil {
		resolvedTarget = filepath.Clean(curr)
	} else {
		resolvedTarget = filepath.Clean(resolvedTarget)
	}

	if !IsSubpath(realRoot, resolvedTarget) {
		return fmt.Errorf("checkSymlinkContainment: path %q escapes root via symlinks to %q", target, resolvedTarget)
	}

	return nil
}

// IsSubpath returns true if target is equal to base or located strictly within base.
// Uses filepath.Rel to eliminate string prefix collision bugs (e.g. /a/b-evil vs /a/b).
func IsSubpath(base, target string) bool {
	b := filepath.Clean(base)
	t := filepath.Clean(target)
	if b == t {
		return true
	}
	rel, err := filepath.Rel(b, t)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// IsDerivedPath reports whether rel refers to a server-managed derived file
// that agents must never write through ValidateMemoryPath.
//
// rel is expected to use forward slashes (the canonical form used in the
// design doc and manifest examples). Callers that have OS-separator paths
// should pass them through filepath.ToSlash first.
//
// The list:
//
//   - meta/index.sqlite and its WAL/SHM companions (the FTS5 shadow index).
//   - meta/lock (the advisory-lock file).
//   - meta/nonces.sqlite and its WAL/SHM companions (the persistent NonceStore).
//
// Files that are server-interpreted but stored as canonical Markdown or YAML
// (manifest.yaml, schema.yaml, index.md) are NOT derived in this sense —
// agents reach them through the normal staged/applied write machinery.
func IsDerivedPath(rel string) bool {
	switch {
	case strings.HasPrefix(rel, "meta/index.sqlite"):
		// Catches index.sqlite, index.sqlite-wal, index.sqlite-shm,
		// index.sqlite-journal, and any future SQLite sidecar.
		return true
	case strings.HasPrefix(rel, "meta/nonces.sqlite"):
		// Catches nonces.sqlite, nonces.sqlite-wal, nonces.sqlite-shm, etc.
		return true
	case rel == "meta/lock":
		return true
	case rel == "meta/nonces.json":
		return true
	}
	return false
}
