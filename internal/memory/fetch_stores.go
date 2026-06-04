package memory

import (
	"path/filepath"

	"github.com/xChuCx/agent-memory/internal/config"
	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

// LoadFetchStores builds the cached-store registry for the fetch search path
// (federation, PR5) from the manifest's declared stores and the committed
// meta/stores.lock. Only stores already materialised into the cache
// (meta/cache/stores/<name>/) are included — a declared-but-unsynced store
// contributes nothing until `agent-memory sync` runs. The lock supplies each
// store's resolved commit for the provenance origin (`name@<short>`); a store
// missing from the lock (or an unlocked local-path store) still federates with
// origin `name`.
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
		origin := st.Name
		if locked, ok := lock.Stores[st.Name]; ok && locked.ResolvedCommit != "" {
			origin = st.Name + "@" + shortCommit(locked.ResolvedCommit)
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
