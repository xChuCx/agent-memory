# Spike S1 — Byte-Preserving Markdown Engine

**Purpose:** Prove that `yuin/goldmark` exposes byte offsets reliably enough to splice Markdown sections without round-tripping through the AST renderer.

**Why this is the bet:** The byte-preserving engine is the single largest technical leverage point in the project. If goldmark can't give us reliable byte offsets, the alternative is a hand-rolled heading parser with code-fence awareness — doable but materially more complex.

## How to run

From the repository root, with Go 1.22+ installed:

```bash
go mod tidy
go test ./spikes/s1-byte-preserving-markdown/...
```

Verbose:

```bash
go test -v ./spikes/s1-byte-preserving-markdown/...
```

All fixtures should pass.

## What each fixture verifies

| Fixture | Verifies |
|---|---|
| `01-replace-section-simple` | Baseline: replace a section by heading text. |
| `02-replace-by-id` | Locate via `<!-- @id: ... -->` anchor. |
| `03-code-fence-with-hash` | `#` lines inside fenced code blocks are not headings. |
| `04-end-of-file-section` | Last section's `ByteEnd = len(src)`. |
| `05-duplicate-headings` | Disambiguate via `occurrence`. |
| `06-nested-headings` | Level-2 consumes level-3 children. |
| `07-html-comment-before-heading` | Unrelated HTML comments don't confuse the anchor finder. |
| `08-yaml-frontmatter` | Frontmatter precedes first heading and is untouched. |

Each fixture directory has:

- `in.md` — input.
- `op.json` — operation to apply (by `section_id` or by `heading` + `heading_level` + `occurrence`).
- `out.md` — expected output.

## What the test driver asserts

1. Output bytes equal `out.md` bytes.
2. `in[:ByteStart]` == `result[:ByteStart]` (prefix preserved).
3. `in[ByteEnd:]` == `result[suffix_start:]` (suffix preserved).

Assertions 2 and 3 are the byte-preservation invariants that justify the entire approach.

## See also

- [Pattern: Byte-Preserving Engine](../../docs/patterns/byte-preserving-engine.md)
- [Spike S1 Results](../../docs/spikes/s1-results.md)
- [Design Doc v0.4.1 §21](../../agent-memory-design-doc-v0.4.1.md)
- [Implementation Plan §3 S1](../../agent-memory-implementation-plan.md)
