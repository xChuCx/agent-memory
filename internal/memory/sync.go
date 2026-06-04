// sync.go materialises referenced "landscape" stores (federation, PR3) into
// the rebuildable cache under .agent-memory/meta/cache/stores/<name>/ and pins
// each to a resolved commit in meta/stores.lock. The pipeline per store is:
//
//	clone (or local-path copy) → sandbox-validate (no symlinks, contained) →
//	secret/PII scan → atomic swap into the cache → record in the lock
//
// No half-synced cache is ever visible (we build in a staging dir and swap),
// and external content is treated as untrusted (its own allowlist markers do
// NOT exempt it from scanning). The index store dimension lands in PR4, so
// sync does not rebuild the index here.
package memory

import (
	"context"
	"fmt"
	iofs "io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xChuCx/agent-memory/internal/config"
	agentfs "github.com/xChuCx/agent-memory/internal/fs"
	"github.com/xChuCx/agent-memory/internal/git"
)

// SyncDeps bundles what Sync needs.
type SyncDeps struct {
	MemoryDir string // absolute path to the consuming repo's .agent-memory/
	Manifest  *config.Manifest
	Logger    *slog.Logger
	// Now is injectable for deterministic tests; nil → time.Now.
	Now func() time.Time
}

func (d SyncDeps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return nopLogger
}

func (d SyncDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// StoreSyncResult reports the outcome of one store. Err is non-nil when that
// store failed; Sync still attempts the others.
type StoreSyncResult struct {
	Name           string
	ResolvedCommit string
	Unlocked       bool
	Err            error
}

// Sync materialises every referenced store and rewrites meta/stores.lock.
// Stores no longer declared in the manifest are reconciled away (their cache
// dir and lock entry are removed). The returned error is for whole-operation
// failures (a malformed lock, an unwritable lock); per-store failures live in
// the results.
func Sync(ctx context.Context, deps SyncDeps) ([]StoreSyncResult, error) {
	_ = ctx
	cacheRoot := filepath.Join(deps.MemoryDir, "meta", "cache", "stores")
	lockPath := filepath.Join(deps.MemoryDir, "meta", config.StoresLockName)

	lock, err := config.LoadStoresLock(lockPath) // fail-closed on malformed/too-new
	if err != nil {
		return nil, err
	}

	// Nothing to do and nothing to maintain → don't create an empty lock
	// (keeps the opt-in invariant: no stores → no new files).
	if len(deps.Manifest.Stores) == 0 && !agentfs.PathExists(lockPath) {
		return nil, nil
	}

	declared := make(map[string]bool, len(deps.Manifest.Stores))
	var results []StoreSyncResult
	for _, st := range deps.Manifest.Stores {
		declared[st.Name] = true
		res := syncOneStore(deps, st, cacheRoot)
		results = append(results, res)
		if res.Err != nil {
			deps.log().Warn("store sync failed", "store", st.Name, "error", res.Err.Error())
			continue
		}
		lock.Stores[st.Name] = config.LockedStore{
			Source:            st.Source,
			RequestedRevision: st.Revision,
			ResolvedCommit:    res.ResolvedCommit,
			ResolvedAt:        deps.now().UTC().Format(time.RFC3339),
			StorePath:         st.StorePath(),
			Unlocked:          res.Unlocked,
		}
		deps.log().Info("store synced", "store", st.Name, "commit", res.ResolvedCommit, "unlocked", res.Unlocked)
	}

	// Reconcile: drop lock entries + cache dirs for undeclared stores.
	for name := range lock.Stores {
		if !declared[name] {
			delete(lock.Stores, name)
		}
	}
	if entries, derr := os.ReadDir(cacheRoot); derr == nil {
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".old")
			name = strings.TrimSuffix(name, ".tmp")
			if e.IsDir() && !declared[name] {
				_ = os.RemoveAll(filepath.Join(cacheRoot, e.Name()))
			}
		}
	}

	if err := config.WriteStoresLock(lockPath, lock); err != nil {
		return results, err
	}
	return results, nil
}

// syncOneStore materialises a single store into cacheRoot/<name>.
func syncOneStore(deps SyncDeps, st config.Store, cacheRoot string) StoreSyncResult {
	res := StoreSyncResult{Name: st.Name}

	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		res.Err = err
		return res
	}
	staging := filepath.Join(cacheRoot, st.Name+".tmp")
	_ = os.RemoveAll(staging)

	var srcStoreDir string
	localExists := agentfs.PathExists(st.Source)
	useGit := !localExists || git.IsWorkTree(st.Source)

	if useGit {
		clone, err := os.MkdirTemp("", "am-sync-")
		if err != nil {
			res.Err = err
			return res
		}
		defer os.RemoveAll(clone)
		repoDir := filepath.Join(clone, "repo")
		if err := git.Clone(st.Source, repoDir); err != nil {
			res.Err = err
			return res
		}
		if st.Revision != "" {
			if err := git.Checkout(repoDir, st.Revision); err != nil {
				res.Err = err
				return res
			}
		}
		commit, err := git.HeadCommit(repoDir)
		if err != nil {
			res.Err = err
			return res
		}
		res.ResolvedCommit = commit
		srcStoreDir = filepath.Join(repoDir, filepath.FromSlash(st.StorePath()))
	} else {
		// Local, non-git path → not reproducible: recorded as unlocked.
		res.Unlocked = true
		srcStoreDir = filepath.Join(st.Source, filepath.FromSlash(st.StorePath()))
	}

	if !agentfs.PathExists(srcStoreDir) {
		res.Err = fmt.Errorf("store path %q not found in source %q", st.StorePath(), st.Source)
		return res
	}

	// Sandbox-copy into staging (rejects symlinks, contains paths).
	if err := agentfs.CopyDirValidated(srcStoreDir, staging); err != nil {
		_ = os.RemoveAll(staging)
		res.Err = fmt.Errorf("validate store %q: %w", st.Name, err)
		return res
	}

	// Secret/PII scan on ingest. Reason codes only — never the matched bytes.
	hits, serr := scanStoreTree(staging, deps.Manifest.Security)
	if serr != nil {
		_ = os.RemoveAll(staging)
		res.Err = serr
		return res
	}
	if len(hits) > 0 {
		_ = os.RemoveAll(staging)
		res.Err = fmt.Errorf("store %q rejected by scan-on-ingest (%d finding(s)): %s",
			st.Name, len(hits), strings.Join(hits, "; "))
		return res
	}

	if err := agentfs.SwapDir(staging, filepath.Join(cacheRoot, st.Name)); err != nil {
		_ = os.RemoveAll(staging)
		res.Err = err
		return res
	}
	return res
}

// scanStoreTree scans the Markdown content of a materialised store using the
// consuming repo's security settings. External allowlist markers are NOT
// honored (Allowlist is left nil) so a referenced store cannot self-exempt
// content from the scan. Returns "rel:line type" descriptions (no secret bytes).
func scanStoreTree(dir string, sec config.Security) ([]string, error) {
	if !sec.SecretScan && !sec.PIIScan {
		return nil, nil // consumer opted out of scanning entirely
	}
	opts := ScanOpts{
		PIIScanSSNAndCC: sec.PIIScan,
		PIIScanEmail:    sec.PIIScanEmail,
	}
	if sec.SecretScan {
		opts.EntropyThreshold = 4.5
		opts.EntropyMinLength = 32
	}

	var hits []string
	err := filepath.WalkDir(dir, func(p string, d iofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		content, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(dir, p)
		for _, f := range Scan(content, opts) {
			hits = append(hits, fmt.Sprintf("%s:%d %s", filepath.ToSlash(rel), f.Line, f.Type))
		}
		return nil
	})
	return hits, err
}
