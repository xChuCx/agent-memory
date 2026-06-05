package memory

import (
	"path/filepath"

	"github.com/xChuCx/agent-memory/internal/config"
	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

// LoadFetchStores builds the cached-store registry for the fetch search path
// (federation, PR5) from the manifest's declared stores and the committed
// meta/stores.lock. A store is federated only when it is BOTH materialised
// (cache dir present) AND recorded in the lock — the federation contract is
// that nothing opaque or unpinned lands, and every chunk's provenance shows the
// store + pin state. So:
//
//   - declared but not synced (no cache dir)     → skipped (run `sync`);
//   - cache dir but no lock entry (orphan)       → skipped (sync reconciles it);
//   - locked with a resolved commit              → origin "name@<short>";
//   - unlocked local-path source (recorded)      → origin "name@unlocked".
//
// Returns (nil, nil) when no stores are declared — the caller then runs the
// single-store path unchanged (the opt-in invariant). A malformed or too-new
// lock returns an error (fail-closed, consistent with sync); callers should
// degrade to a local-only fetch rather than fail the whole request.
func LoadFetchStores(memDir string, manifest *config.Manifest) ([]StoreRef, error) {
	if manifest == nil || len(manifest.Stores) == 0 {
		return nil, nil
	}
	lock, err := config.LoadStoresLock(filepath.Join(memDir, "meta", config.StoresLockName))
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Join(memDir, "meta", "cache", "stores")

	var refs []StoreRef
	for _, st := range manifest.Stores {
		dir := filepath.Join(cacheRoot, st.Name)
		if !agentfs.PathExists(dir) {
			continue // declared but not synced yet
		}
		locked, ok := lock.Stores[st.Name]
		if !ok {
			continue // materialised but unrecorded — not a pinned/reviewable store
		}
		var origin string
		switch {
		case locked.ResolvedCommit != "":
			origin = st.Name + "@" + shortCommit(locked.ResolvedCommit)
		case locked.Unlocked:
			origin = st.Name + "@unlocked" // local-path: recorded but not commit-pinned
		default:
			continue // lock entry with neither a commit nor unlocked → skip
		}
		refs = append(refs, StoreRef{
			Name:               st.Name,
			Dir:                dir,
			Origin:             origin,
			PriorityMultiplier: st.Priority(),
		})
	}
	return refs, nil
}

// shortCommit abbreviates a resolved commit SHA for the provenance label.
func shortCommit(c string) string {
	const n = 12
	if len(c) > n {
		return c[:n]
	}
	return c
}
