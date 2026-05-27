# Pattern: Section-Schema Enforcement (Maturity)

**Status:** Implemented in [`internal/schema/validate_section.go`](../../internal/schema/validate_section.go), [`internal/schema/schema.go`](../../internal/schema/schema.go), wired through [`internal/memory/update.go`](../../internal/memory/update.go).
**Owner:** `internal/schema/` + `internal/memory/` (Release 0.3).
**Builds on:** [Pattern: Security Layer](security-layer.md), [Pattern: propose_update Pipeline](propose-update-pipeline.md).

## Problem

v0.1.0 shipped the `SectionSchema` validator (per-section
required/optional fields with regex + enum checks) but no category in
`DefaultSchema()` declared one. The machinery existed; nothing used it.
Result: structural correctness was enforced (markdown parses, anchor
IDs assigned), but semantic correctness — "every decision has a Date,
Status, and Confidence" — was not.

Three obstacles to turning the dormant validator on:

1. **Parser bias.** The author convention in every existing adapter
   doc (SKILL.md, AGENTS.md, GEMINI.md, cursor MDC) shows fields as
   `**Date:** 2026-05-27` (markdown bold). The original
   `parseFieldLines` skipped lines starting with `*` as bullets and
   wouldn't parse them.
2. **Migration cost.** Once a category has a SectionSchema, every
   touched section is validated. Legacy decisions written before the
   schema landed (no Date/Status/Confidence) would fail validation on
   the next unrelated propose_update.
3. **Parent-expansion artefact.** When `append_section` adds a child
   under a parent, the parent's full content range expands (it now
   includes the new child). A naive "section is affected if its
   ContentHash changed" check would re-validate the parent —
   incorrectly, because the parent's authored body didn't change.

This kit closes all three.

## Solution

### Parser: bold + italic + plain

`parseFieldLines` (`internal/schema/validate_section.go`) now treats
all three shapes equivalently:

```
Date: 2026-05-27
**Date:** 2026-05-27        ← markdown bold
*Date:* 2026-05-27          ← markdown italic
```

Bullet detection requires a mandatory space after the marker:
`- foo` and `* foo` are bullets and skipped; `**bold**` is not because
there's no space separating `*` from content.

After name + value extraction, leading/trailing `*` runs are trimmed
from both halves with `strings.Trim`, so `value` doesn't carry the
closing emphasis markers as syntactic noise.

### Real SectionSchema for `decisions`

`DefaultSchema()` now declares:

```yaml
decisions:
  section_schema:
    per_section_required_fields:
      - name: Date
        pattern: ^\d{4}-\d{2}-\d{2}$
      - name: Status
        enum: [active, superseded, deprecated, proposed]
      - name: Confidence
        enum: [confirmed, inferred, user-provided]
```

Three fields, all required, two with enums. Lower-case enum values
match standard YAML/JSON conventions; adapter docs updated to match.

### Affected-only validation

The orchestrator (`internal/memory/update.go`) used to validate ALL
sections of every touched file. That would have broken every legacy
deployment on the first propose_update post-upgrade. The new logic:

```
isWholeFileNew = pre-state was empty
for each section in post-state:
    affected = isWholeFileNew
              || section.AnchorID was not in pre-state
              || directBody(section, post) != directBody(matching section, pre)
    if not affected: skip
    validate body against cat.SectionSchema
```

Key word: `directBody`. For a section at `sections[idx]`, the direct
body is the bytes from `findSectionBodyStart` to either the first
**descendant** section's start, or the section's own ByteEnd if it has
no descendants. This excludes nested children entirely — a parent's
"direct body" doesn't include its sub-sections.

### Why directBody (not just ContentHash)

Concrete example. A decisions.md file:

```
# Decisions           ← anchor: decisions, level 1
<!-- @id: decisions -->

(no body fields here, top-level introduction)

## Use Postgres       ← anchor: use-postgres, level 2
<!-- @id: use-postgres -->

**Date:** 2026-05-27
...
```

Pre-state: just the `# Decisions` heading with no children.
After `append_section` adds `## Use Postgres`:

- `# Decisions` section's **ContentHash changed** (its byte range now
  includes the new level-2 child).
- `# Decisions` section's **directBody is unchanged** (heading +
  anchor + intro prose, stopping at the new child).

The directBody check correctly classifies `# Decisions` as
"unaffected" (the author of this proposal didn't touch its body) and
the new `## Use Postgres` as "new" (its anchor wasn't in pre-state).

Without directBody, we'd re-validate `# Decisions` for the new
Date/Status/Confidence fields — which it shouldn't have at the file's
top level.

## Migration story for existing deployments

Users upgrading from v0.2.0:

1. **Existing `meta/schema.yaml`** in their repo was written by v0.2.0's
   `agent-memory init`; it has no SectionSchema for decisions. **No
   change in behaviour** until they regenerate or manually add the
   schema.
2. **Fresh `agent-memory init`** (post-upgrade) writes the new
   DefaultSchema. New deployments enforce decision schema from day
   one.
3. **`agent-memory init --force`** on an existing repo regenerates
   the schema; legacy decisions in `decisions.md` are NOT touched
   (affected-only validation skips them) until the user edits them
   via propose_update.
4. **Manual opt-in for existing repos**: edit `meta/schema.yaml`,
   add the `section_schema:` block from DefaultSchema. The same
   affected-only logic kicks in; existing decisions stay valid until
   modified.

This is forward-only safe.

## What ValidateSection still does NOT enforce

- **File-level invariants.** `RequiredTopLevelHeading: true` in
  SectionSchema is honoured by the loader but the orchestrator
  doesn't run a file-level check. A category whose first heading
  isn't level-1 is currently accepted. (Future M3 batch.)
- **Field uniqueness.** The parser stores the first occurrence per
  name. A section with two `Date:` lines doesn't fire a "duplicate"
  violation; the second is ignored.
- **Inter-field dependencies.** "If Status=superseded then
  Replaced-By is required" — not expressible in the current
  FieldSpec shape. Would need a richer schema type.
- **Cross-section invariants.** "Each module file has an Overview
  + Internals section" — section schema is per-section, not per-file.
- **Order constraints.** "Date must appear before Status" — not
  enforced; the parser just collects fields.

These are intentional simplifications. Real ADR / decision-log tools
have richer schemas (e.g., MADR's structured templates); we kept the
shape narrow enough to be useful without bikeshedding the perfect
template.

## Opt-in for other categories

Users who want strict schemas for modules, pitfalls, or conventions
edit their `meta/schema.yaml`:

```yaml
categories:
  modules:
    section_schema:
      per_section_required_fields:
        - name: Last-Reviewed
          pattern: ^\d{4}-\d{2}-\d{2}$
      per_section_optional_fields:
        - name: Owner
        - name: Status
          enum: [stable, evolving, deprecated]
```

The same affected-only logic applies. Add the schema, edit the file,
the orchestrator picks up the policy on next propose_update.

The two-step `LoadSchema` merge (documented in
[configuration-loading.md](configuration-loading.md)) supports this:
adding a `section_schema:` block in your `meta/schema.yaml` merges
into the default category fields without rewriting them.

## References

- [Design Doc v0.4.1 §25.2 (SectionSchema)](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.x (Section schema maturity)](../../agent-memory-implementation-plan.md).
- [Pattern: propose_update Pipeline](propose-update-pipeline.md) — the
  pipeline that runs section validation.
- [Pattern: Configuration Loading](configuration-loading.md) — how
  manifest + schema merge resolves defaults vs overrides.
- MADR (Markdown ADRs) — https://adr.github.io/madr/ — a richer
  template format we deliberately don't replicate.
