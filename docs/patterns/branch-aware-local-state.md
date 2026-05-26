# Pattern: Branch-Aware Local State

**Status:** Branch resolver implemented in [`internal/git/branch.go`](../../internal/git/branch.go). Consumption by the fetch pipeline (per-branch `local/current.*.md` selection) lands in T2.7.
**Owner:** `internal/git/` (M2 read path); future `internal/git/commit.go` for the write side (M7).
**Tracks design:** [Design Doc v0.4.1 §13](../../agent-memory-design-doc-v0.4.1.md).

## Problem

The agent's "current task" state is per-branch. When the developer switches branches the agent should see different `local/current.<branch>.md` content; when there is no git repo at all, the agent should still work (just with a single shared file).

The resolver has two jobs:

1. Tell the fetch pipeline what file to load for the current branch.
2. Stay cheap — every `memory.fetch_context` call hits this path.

## Solution

A thin shell-out around the system `git` binary. Two reads cover every case we care about:

```
git -C <root> rev-parse --is-inside-work-tree
git -C <root> rev-parse --abbrev-ref HEAD
git -C <root> rev-parse --short HEAD     # only when HEAD is detached
```

The output is wrapped in `BranchInfo`:

```go
type BranchInfo struct {
    Name       string // empty when IsDetached
    ShortSHA   string // populated when IsDetached
    IsDetached bool
    IsGitRepo  bool   // false when root isn't inside any work tree
}

func ActiveBranch(root string) (BranchInfo, error)
```

States the caller observes:

| State | Name | ShortSHA | IsDetached | IsGitRepo |
|---|---|---|---|---|
| Normal branch | "main" / "feature/auth" / ... | "" | false | true |
| Detached HEAD | "" | "a1b2c3" | true | true |
| Not a git repo | "" | "" | false | false |
| `git` not on PATH | (returns ErrGitNotInstalled) | | | |

The "not a git repo" case is not an error — the agent-memory layout in that environment uses a single shared `local/current.shared.md` rather than per-branch files.

## SlugBranch

Branch names contain characters that aren't safe in path components: `/`, `:` (Windows), occasional unicode. `SlugBranch` collapses to the canonical form used in `local/current.<slug>.md`:

- Lowercase.
- `[a-z0-9]` survive.
- Everything else becomes a `-`.
- Runs of dashes collapse to one.
- Leading and trailing dashes trimmed.

```
main                      → main
feature/auth-rotation     → feature-auth-rotation
Bugfix/JIRA-123           → bugfix-jira-123
release/2026.05.01        → release-2026-05-01
```

The rules are identical in spirit to `markdown.slugify` (the section ID generator), but the two live in separate packages to avoid coupling unrelated concerns. The rule set is small enough that the duplication doesn't matter.

## Performance / caching

`ActiveBranch` shells out twice on every call (three times for detached HEAD). On a hot loop (`fetch_context` called many times per session), this becomes meaningful: each `git rev-parse` is ~5-15ms on Windows. The M2 fetch pipeline caches the result per `*MemoryContext` so a single CLI invocation or MCP request resolves the branch once.

Long-running daemons (the MCP server) refresh the cache on every request because the user may have `git checkout`'d between requests. Cheaper than the alternative of watching `.git/HEAD` for changes.

## What this does NOT cover

- **Branch creation / switching.** That's the developer's job via `git checkout`; we only read.
- **Worktrees with separate HEADs.** Git's worktree feature makes each worktree directory have its own HEAD ref. `ActiveBranch(workTreeRoot)` returns the right answer because `git -C` follows the work tree, not the main repo.
- **Submodules.** Treated as their own work trees if the caller passes the submodule root; otherwise reported as the parent's branch. Not specifically tested.
- **`git` over SSH or networked repos.** Local reads only.

## References

- [Design Doc v0.4.1 §13](../../agent-memory-design-doc-v0.4.1.md) — per-branch local state model.
- [Implementation Plan §6.2 T2.1](../../agent-memory-implementation-plan.md).
- `git rev-parse` documentation — the underlying primitive.
