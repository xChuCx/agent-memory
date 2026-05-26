// Package fs provides filesystem helpers for the agent-memory project:
//
//   - Atomic writes (atomic.go): write a file such that readers always see
//     either the pre-write or post-write contents, never a partial state.
//   - Path validation (paths.go): refuse paths that escape the .agent-memory/
//     root or target server-managed derived files.
//
// See docs/patterns/atomic-writes.md and docs/patterns/path-validation.md.
package fs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// WriteAtomic writes data to path atomically using the canonical
// write-temp-then-rename pattern:
//
//  1. Create a temp file in the same directory as path (cross-device renames
//     are not atomic on POSIX).
//  2. Write all of data, then fsync the temp file.
//  3. Rename the temp file to path. Both NTFS and POSIX guarantee rename is
//     atomic when source and destination are on the same filesystem.
//  4. On POSIX, fsync the parent directory so the rename survives power loss.
//     Windows skips this step (NTFS handles rename durability internally and
//     directory handles do not support Sync there).
//
// Readers of path always see either the previous contents or the new
// contents, never a partial write. If any step fails, the temp file is
// removed and the original target (if any) is left untouched.
//
// Constraints:
//   - path must be absolute.
//   - The parent directory must exist.
//   - perm is applied to both the temp file (briefly) and the final file.
func WriteAtomic(path string, data []byte, perm fs.FileMode) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("WriteAtomic: path must be absolute: %q", path)
	}
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("WriteAtomic: stat parent: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("WriteAtomic: parent is not a directory: %q", dir)
	}

	tmpPath, err := makeTempPath(path)
	if err != nil {
		return fmt.Errorf("WriteAtomic: %w", err)
	}

	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("WriteAtomic: open temp: %w", err)
	}

	// If we don't reach the successful rename, remove the temp file.
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("WriteAtomic: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("WriteAtomic: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("WriteAtomic: close temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("WriteAtomic: rename: %w", err)
	}
	// Rename succeeded; the temp file no longer exists.
	cleanup = false

	syncDir(dir)
	return nil
}

// makeTempPath returns a unique temp path in the same directory as path.
// The format is "<path>.tmp.<8 random bytes hex-encoded>" so concurrent
// writers do not collide.
func makeTempPath(path string) (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("random nonce: %w", err)
	}
	return path + ".tmp." + hex.EncodeToString(nonce[:]), nil
}

// syncDir fsyncs the directory at dir on POSIX. On Windows it is a no-op:
// NTFS does not support fsync on directories and rename durability is
// handled by the filesystem itself. Best-effort either way.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}

// PathExists is a small convenience used by callers and tests.
func PathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !errors.Is(err, os.ErrNotExist)
}
