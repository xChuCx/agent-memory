# Pattern: federation — referenced stores (manifest + lockfile)

**Scope:** PR2 of the federation slice — the *declaration + pinning* contract
only. Sync (PR3), the index store dimension (PR4), multi-store fetch (PR5), and
the retrieval eval (PR6) build on this. Full design:
[docs/design/federated-memory.md](../design/federated-memory.md).

## Problem

A repo's `.agent-memory/` knows only itself. An agent designing a cross-service
feature needs the surrounding system map. Federation lets a repo **reference**
shared "landscape" stores (a platform/architecture-memory repo). This pattern
covers how a reference is declared and pinned — not yet how it is fetched.

## The manifest `stores` block

`config.Store` (in `internal/config/stores.go`), under `manifest.yaml` →
`stores`:

```yaml
stores:
  - name: platform                  # slug ^[a-z0-9][a-z0-9-]*$ — a cache dir + provenance label
    source: https://github.com/...  # git URL or local path (required)
    revision: v2025.06              # branch/tag/commit; default branch if empty
    path: .agent-memory             # store dir within the repo (default)
    mode: read-only                 # only mode in slice 1
    priority_multiplier: 0.8        # ranking multiplier vs local 1.0
```

Validation (`validateStores`, wired into `Manifest.Validate`): unique safe-slug
names, non-empty source, recognised mode, a **positive priority when set** (omit
`priority_multiplier` to use the default 0.8), and a safe relative `path`
(forward-slash, clean, no `..`, no drive letter).

**`priority_multiplier` and the negative-BM25 sign.** It multiplies the existing
score from `internal/index/ranking.go`, where BM25 is **negative** (more-negative
= better). So `1.0` is neutral (local), `<1` **penalizes**, `>1` boosts. Landscape
defaults to `0.8` so local wins ties. Do not "fix the sign".

## The lockfile — `meta/stores.lock`

`config.StoresLock` (`internal/config/stores_lock.go`): a **committed**, versioned
file pinning each store to a `resolved_commit` (analogous to `go.sum`) so a
team/CI sees identical landscape memory. The materialised copy lives under
`meta/cache/stores/<name>/` — **gitignored, rebuildable** (consistent with
`meta/index.sqlite`). A local-path source that is not a git work tree is recorded
`unlocked` (not reproducible; dev/monorepo only). PR2 ships the lockfile I/O;
PR3 (`sync`) populates it.

## Minimal landscape schema

The default schema gains three structured kinds (`internal/schema`):
`component` (`components.md`), `contract` (`contracts.md`, required enum fields
`Kind`/`Direction`), `actor` (`actors.md`). They are **authored only in a
landscape store**; a normal repo never creates these files, so the categories are
inert there. Declaring them in the default schema keeps one schema (no variants).

## Opt-in invariant

With **no** `stores` declared, behavior is byte-for-byte the single-store path —
the `stores:` key is omitted from a fresh manifest, and every PR in the slice
carries a regression test asserting this.

## CLI

`agent-memory store add|list|rm` edits the manifest's `stores`; `status` lists
declared stores and their lock state (`not synced` until PR3).

## Deliberately deferred (later PRs)

Fetching a store, the index `store` dimension, per-store-fair retrieval, and the
**security model** (symlink/path-escape sandboxing on sync; external content as
an untrusted, provenance-labeled trust boundary) land with sync (PR3) and fetch
(PR5). See the design doc §6.2, §7.
