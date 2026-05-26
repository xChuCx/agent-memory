# Pattern: Configuration Loading

**Status:** Implemented in [`internal/config/manifest.go`](../../internal/config/manifest.go) and [`internal/schema/schema.go`](../../internal/schema/schema.go).
**Owner:** `internal/config/`, `internal/schema/` (M1).
**Tracks design:** [Design Doc v0.4.1 §25, §26](../../agent-memory-design-doc-v0.4.1.md).

## Problem

Two YAML files in `.agent-memory/meta/`:

| File | Concern |
|---|---|
| `manifest.yaml` | Operational settings — budgets, staging TTL, security flags, git policy, per-operation approval overrides. Project-specific tuning. |
| `schema.yaml` | Category definitions — file/glob patterns, per-category approval defaults, provenance rules, section-level structural requirements. Largely stable across projects. |

We need to:

1. Load each into typed Go structs.
2. Apply sensible defaults so users can omit anything they don't want to customise.
3. Validate the merged result before downstream code consumes it.
4. Write the recommended versions atomically during `agent-memory init`.

## Solution

`gopkg.in/yaml.v3 Unmarshal` into a struct that's pre-populated with `Default*()`. yaml.v3's merge semantics on plain structs work the way we want: fields present in YAML overwrite defaults; fields absent leave the default untouched. This is what `LoadManifest` relies on.

```go
func LoadManifest(path string) (*Manifest, error) {
    b, err := os.ReadFile(path)
    if err != nil { return nil, err }
    m := DefaultManifest()
    if err := yaml.Unmarshal(b, m); err != nil { return nil, err }
    return m, nil
}
```

Writes use [`internal/fs.WriteAtomic`](atomic-writes.md) so readers always see either the pre-write or post-write version.

## yaml.v3 does NOT merge into existing map values

The naive pattern above breaks for `Schema.Categories` (a `map[string]Category`). When yaml.v3 unmarshals a YAML map key that already exists in the Go map, it creates a fresh zero-value `Category{}` and unmarshals INTO it, then assigns to the map. The existing value (with its defaults) is **discarded**.

Result: a YAML file with

```yaml
categories:
  decisions:
    approval: apply
```

would leave `decisions.File` as `""` (zero value) — the default `decisions.md` is lost.

`LoadSchema` works around this with a **two-step custom merge**:

1. Decode the YAML into a fresh `Schema` (no defaults).
2. Start from `DefaultSchema()` and merge field-by-field for each category that the user mentioned. Categories not mentioned survive verbatim from defaults; user-defined categories not in defaults are accepted as-is.

```go
func LoadSchema(schemaPath string) (*Schema, error) {
    var loaded Schema
    yaml.Unmarshal(file, &loaded)
    s := DefaultSchema()
    if loaded.Version != "" { s.Version = loaded.Version }
    for name, lcat := range loaded.Categories {
        if dcat, ok := s.Categories[name]; ok {
            s.Categories[name] = mergeCategory(dcat, lcat)
        } else {
            s.Categories[name] = lcat
        }
    }
    return s, nil
}
```

`mergeCategory` overrides each field on `defaults` only if the corresponding value in `loaded` is non-zero.

### Bool-flip limitation

Go's zero value for `bool` is `false`. yaml.v3 cannot distinguish an explicit `field: false` in YAML from "field not set". `mergeCategory` therefore treats a `false` in loaded as "not set" and preserves the default.

In practice: a user can flip a default-`false` bool to `true` via partial override. To flip a default-`true` bool to `false`, the user must write the full category structure rather than the partial-override style.

This is documented in `TestLoadSchema_BoolFalseToTrueOverrideWorks` (the supported case) and `TestLoadSchema_BoolFlipTrueToFalseDoesNotWork` (the limitation). The latter test is a regression guard: if our merge ever changes to support boolean flips both ways, the test fails and forces a doc update at the same time.

## Package layout and dependency direction

```
internal/schema   ← defines ApprovalMode and per-category structure
   ▲
   │ imports
   │
internal/config   ← manifest references ApprovalMode for per-operation overrides
```

`internal/config` imports `internal/schema`, never the reverse. `ApprovalMode` lives in the schema package because approval modes are a schema concept (the set of legal values is intrinsic to the model); the manifest just *uses* the type to express per-operation overrides.

## ApprovalMode

```go
package schema

type ApprovalMode string

const (
    ApprovalApply       ApprovalMode = "apply"
    ApprovalStage       ApprovalMode = "stage"
    ApprovalServerOnly  ApprovalMode = "server_only"
)
```

Each `Category` in the schema declares its default approval mode. The manifest's `updates.approval` block then provides per-operation overrides (`pitfalls_append` vs `pitfalls_replace`, `current` vs `current_shared`, etc.) that can't be expressed at the per-file granularity.

## CategoryForPath

```go
cat, ok := s.CategoryForPath("modules/auth.md")
// cat.Name == "modules", cat.FileGlob == "modules/*.md", cat.Approval == ApprovalStage
```

Lookup order:

1. Exact `File` match (e.g., `decisions.md` → decisions category).
2. `FileGlob` match via `path.Match` (e.g., `modules/*.md` → modules category).

**`path.Match`, not `path/filepath.Match`.** The `path` package always treats `/` as the separator on every OS. `path/filepath` uses the OS-native separator (`\` on Windows), which lets `*` span what we semantically consider a directory boundary — `modules/auth/extra/deep.md` would match `modules/*.md` on Windows. Our memory-relative paths are always forward-slash by convention, so `path.Match` is the correct primitive. `TestCategoryForPath_NoMatch/modules/auth/extra/deep.md` guards against regressions on Windows runners.

`Category.Name` is populated from the map key by `populateCategoryNames()` so callers don't have to thread the lookup key separately.

## Defaults

Defaults track design doc v0.4.1 §25.1 and §26.1. Highlights:

**Manifest:**

| Field | Default |
|---|---|
| `budgets.bootstrap_chars` | 12000 |
| `budgets.fetch_context_chars` | 24000 |
| `budgets.max_file_chars` | 20000 |
| `updates.approval.decisions` | stage |
| `updates.approval.pitfalls_append` | apply |
| `updates.approval.current` | apply |
| `updates.approval.index` | server_only |
| `staging.ttl_seconds` | 604800 (7 days) |
| `security.secret_scan` | true |
| `archive.stale_threshold_days` | 60 |
| `concurrency.wait_timeout_seconds` | 10 |
| `local_state.per_branch` | true |
| `git.commit_message_prefix` | `chore(memory):` |

**Schema categories (default approval / git-tracked):**

| Category | Match | Approval | GitTracked |
|---|---|---|---|
| index | `index.md` | server_only | true |
| conventions | `conventions.md` | stage | true |
| decisions | `decisions.md` | stage | true |
| pitfalls | `pitfalls.md` | apply (append default) | true |
| modules | `modules/*.md` | stage | true |
| archive | `archive/*.md` | stage (write-once) | true |
| current | `local/current.*.md` | apply | false |
| sessions | `sessions/*.md` | apply | false |

## Concurrency.LockTTLSeconds is intentionally ignored

The manifest still accepts `concurrency.lock_ttl_seconds` from legacy v0.4 files for graceful upgrades, but [v0.4.1 §11](../../agent-memory-design-doc-v0.4.1.md) replaced TTL-based locking with OS-level advisory locks. The kernel handles release on process death; the application has no TTL clock to enforce. The field is YAML-tagged `omitempty` so freshly written manifests don't carry it forward.

`TestLoadManifest_LegacyLockTTLAcceptedAndIgnored` documents this: the value round-trips through the struct but is never read by `internal/lock`.

## Validation

Both `Manifest` and `Schema` expose a `Validate() error` method that runs basic invariants:

- Version is non-empty.
- Approval modes (where present) are recognised values.
- Budgets / TTL values are positive.
- Schema categories declare exactly one of `File` or `FileGlob`.
- Schema categories don't claim both `server_managed: true` and `agent_writable: true`.

Heavier semantic checks (e.g., that the manifest's per-category override is *compatible* with the schema's category definition) live in downstream code, not the loaders.

## What's deferred

- **Section-schema enforcement.** Per-field patterns and enums (`per_section_required_fields[].pattern`, `enum`) are stored verbatim by the loader; the validator that consumes them lands in M3 alongside `propose_update` schema checks.
- **User-defined categories beyond the built-ins.** yaml.v3's merge semantics already accept any category name not in defaults (it gets added to the map on load), but no downstream code routes against such categories yet.
- **Schema migrations.** When the schema's `version` bumps, current code treats older versions as forward-compatible. M3+ will add migration helpers per design doc §25.3.

## References

- [Design Doc v0.4.1 §25, §26](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §5.2 T1.8, T1.9](../../agent-memory-implementation-plan.md).
- [Atomic Writes pattern](atomic-writes.md) — used by both `WriteManifest` and `WriteSchema`.
- [yaml.v3](https://github.com/go-yaml/yaml/tree/v3) — the YAML library and its merge-into-existing-struct behaviour.
