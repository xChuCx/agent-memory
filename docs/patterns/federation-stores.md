# Pattern: federation — referenced stores (manifest + lockfile)

**Scope:** the federation slice's *declaration + pinning* contract (introduced
in PR2). Sync (PR3) and the index `store` dimension (PR4,
[shadow-index pattern](sqlite-fts5-shadow-index.md#federation-the-store-dimension-schema-v2))
build on it and have landed; multi-store fetch (PR5) and the retrieval eval
(PR6) are still to come. Full design:
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
`unlocked` (not reproducible; dev/monorepo only). It is **authoritative**:
`agent-memory sync` re-materialises a pinned store at its locked commit; the pin
moves forward only with `--update` or a changed `revision` (below).

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
declared stores and their lock state (`not synced` until `agent-memory sync`).

## Sync lifecycle (`agent-memory sync`)

`memory.Sync` materialises each declared store into the cache and writes the
lock. Per store:

1. **Resolve the commit.** For a git source, reproduce the lock's pinned
   `resolved_commit` — so a team/CI gets identical landscape memory — unless
   `--update` is passed or the manifest's `revision` changed, in which case the
   requested revision is resolved fresh and re-pinned. A local non-git path is
   taken in place and recorded `unlocked` (a `revision` on such a source is an
   error).
2. **Clone** the source into a throwaway temp dir and check out that commit.
3. **Validate** it is an agent-memory store: `meta/manifest.yaml` must load
   (which applies the store-format-version guard — a too-new store **fails
   closed**) and pass manifest validation.
4. **Sandbox-validate + copy** the store dir (`<repo>/<path>`) into a staging
   dir: `fs.CopyDirValidated` rejects symlinks (never follows them), keeps every
   path under the destination, and copies regular files only.
5. **Scan on ingest**: text files (`.md`, `.yaml`/`.yml`, `.json`, `.txt`) are
   secret/PII-scanned with the *consuming* repo's `security` settings; a store's
   own allowlist markers are **not** honored (it cannot self-exempt). Any
   finding rejects that store (reason codes only — never secret bytes).
6. **Swap** the staging dir into `meta/cache/stores/<name>/` (`fs.SwapDir`,
   Windows-safe two-step). This is not fully atomic — there is a brief window
   where the cache dir is absent — which is fine while nothing reads the cache
   concurrently; PR5 (fetch) will coordinate via a shared lock.
7. **Record** the resolved commit + timestamp in `stores.lock`.

A failed store is reported and skipped; the others still sync. Stores removed
from the manifest are **reconciled** out of both the lock and the cache. `sync`
does not touch the agent's context or rebuild the index.

## Deliberately deferred (later PRs)

Per-store-fair multi-store **fetch** (PR5, with provenance + the
untrusted-context trust boundary at *read* time) and the multi-store retrieval
eval (PR6). See the design doc §6.2, §7. (The index `store` dimension these
build on landed in PR4 — see the
[shadow-index pattern](sqlite-fts5-shadow-index.md#federation-the-store-dimension-schema-v2).)
