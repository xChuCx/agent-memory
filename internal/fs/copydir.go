package fs

import (
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrSymlinkNotAllowed is returned by CopyDirValidated when it encounters a
// symlink anywhere in the source tree. Synced external stores must not contain
// symlinks — they could escape the store root or point at host files — so we
// reject rather than follow them.
var ErrSymlinkNotAllowed = errors.New("symlink not allowed in store")

// CopyDirValidated copies the directory tree at src to dst, enforcing a sandbox
// suitable for untrusted external content (a referenced store being synced):
//
//   - symlinks are rejected and never followed (ErrSymlinkNotAllowed);
//   - only directories and regular files are copied (devices/pipes/sockets are
//     rejected);
//   - every destination path is verified to stay under dst.
//
// dst must not already exist; its parent must exist.
func CopyDirValidated(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("CopyDirValidated: stat src: %w", err)
	}
	if info.Mode()&iofs.ModeSymlink != 0 {
		return fmt.Errorf("CopyDirValidated: %q: %w", src, ErrSymlinkNotAllowed)
	}
	if !info.IsDir() {
		return fmt.Errorf("CopyDirValidated: src is not a directory: %q", src)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}

	return filepath.WalkDir(src, func(p string, d iofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// WalkDir reports a symlink via its entry type without following it.
		if d.Type()&iofs.ModeSymlink != 0 {
			return fmt.Errorf("%q: %w", p, ErrSymlinkNotAllowed)
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		// Defensive containment check (WalkDir rel is already clean under src).
		ta, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		if ta != dstAbs && !strings.HasPrefix(ta, dstAbs+string(os.PathSeparator)) {
			return fmt.Errorf("path %q escapes destination", rel)
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyRegularFile(p, target)
		default:
			return fmt.Errorf("unsupported file type for %q", rel)
		}
	})
}

func copyRegularFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// SwapDir atomically replaces dest with staging (both must be on the same
// filesystem). Windows-safe: a rename onto an existing directory fails there,
// so the existing dest is moved aside first and removed only after the swap
// succeeds; on failure the original is restored. No half-synced state is ever
// visible at dest.
func SwapDir(staging, dest string) error {
	old := dest + ".old"
	_ = os.RemoveAll(old)
	if PathExists(dest) {
		if err := os.Rename(dest, old); err != nil {
			return fmt.Errorf("SwapDir: move existing aside: %w", err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		if PathExists(old) {
			_ = os.Rename(old, dest) // best-effort rollback
		}
		return fmt.Errorf("SwapDir: rename staging into place: %w", err)
	}
	_ = os.RemoveAll(old)
	return nil
}
