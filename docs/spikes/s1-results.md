# Spike S1 — Byte-Preserving Markdown Engine

**Status:** Validated. Decision: **GO** with one defect found and fixed.
**Started:** 2026-05-26
**Closed:** 2026-05-26
**Goal:** Prove `yuin/goldmark` exposes byte offsets reliably enough to splice Markdown sections without round-tripping through the renderer.

## Decision: GO

Goldmark's AST + manual byte-offset bookkeeping is sufficient for the engine. First validation run revealed one bug in the `@id` anchor finder (not in goldmark itself). The fix was a few lines and added regression tests. With the fix in place, the approach is approved for the M1 implementation in `internal/markdown/`.

## How to validate

From the repository root, on a machine with Go 1.22+ installed:

```bash
go mod tidy
go test ./spikes/s1-byte-preserving-markdown/...
```

Expected: all `TestFixtures/*` subtests pass plus `TestParseSectionsBasic`.

For more verbose output:

```bash
go test -v ./spikes/s1-byte-preserving-markdown/...
```

## Method

Each fixture lives under `spikes/s1-byte-preserving-markdown/testdata/<NN-name>/` with three files:

| File | Purpose |
|---|---|
| `in.md` | Input Markdown source. |
| `op.json` | The operation to apply (section_id-based or heading-based). |
| `out.md` | Expected output bytes after applying the operation. |

The test driver:

1. Parses `in.md` into `[]Section` via `ParseSections`.
2. Locates the target section per `op.json`.
3. Splices in `op.content`.
4. Asserts byte-equality of result with `out.md`.
5. Asserts byte-equality of `in[:ByteStart]` with `result[:ByteStart]` (prefix preservation).
6. Asserts byte-equality of `in[ByteEnd:]` with `result[suffixStart:]` (suffix preservation).

Assertions 5 and 6 are the byte-preservation invariants that justify this entire approach.

## Fixture matrix

| # | Name | What it tests | Initial run | After fix |
|---|---|---|---|---|
| 01 | `replace-section-simple` | Baseline: ATX h2 replacement by heading text. | PASS | PASS |
| 02 | `replace-by-id` | `<!-- @id: ... -->` anchor lookup. | **FAIL** | PASS |
| 03 | `code-fence-with-hash` | `#` lines inside fenced code blocks are not parsed as headings. | PASS | PASS |
| 04 | `end-of-file-section` | Last section's `ByteEnd = len(src)`; trailing newline preserved. | PASS | PASS |
| 05 | `duplicate-headings` | Disambiguate via `occurrence` field. | PASS | PASS |
| 06 | `nested-headings` | Level-2 section subsumes level-3 children; `@id` lookup of parent. | PASS | PASS |
| 07 | `html-comment-before-heading` | Unrelated HTML comments don't confuse the anchor finder. | PASS | PASS |
| 08 | `yaml-frontmatter` | Frontmatter precedes first heading and is untouched by splice. | PASS | PASS |
| 09 | `multiple-anchors` | Three anchored sections in one file; lookup picks the right one. | (added) | PASS |

## Deferred fixtures (Phase 2)

| Case | Why deferred |
|---|---|
| CRLF preservation | Requires fixture-write tooling that doesn't auto-convert line endings on Windows. Will add via Go test-time fixture generation in M1. |
| BOM preservation | Same reason. Splice algorithm trivially preserves BOM (it's in the prefix), so this is a fixture-tooling issue, not an engine concern. |
| Setext heading style | Out of scope per design doc; we only emit ATX. Adding only if real-world inputs exhibit it. |
| Inline HTML in heading text | Out of scope for MVP. |

## Findings (running notes)

### 2026-05-26 — Initial implementation

Code: `spikes/s1-byte-preserving-markdown/splice.go` (~170 lines), `splice_test.go` (~90 lines).

Approach choices, captured in [byte-preserving-engine.md](../patterns/byte-preserving-engine.md):

- Goldmark AST walk for heading discovery.
- Manual backward scan from `Lines().At(0).Start` to find heading-line start byte.
- Section end = "next heading at same or higher level, or `len(src)`".
- `@id` anchor finder used bounded forward scan (256 bytes) after each heading line. **This turned out to be wrong (see next entry).**

### 2026-05-26 — First validation run (user's machine, Go 1.22+)

Ran `go test -v ./spikes/s1-byte-preserving-markdown/...` against the initial implementation.

**Results:**

- 7 of 8 fixtures PASS.
- 1 fixture FAIL: `02-replace-by-id`.
- All 3 sub-suites of `TestAnchorIDExtraction` (5 cases) PASS.
- `TestParseSectionsBasic` PASS.
- `TestSpliceRangeValidation` PASS.

**Root cause of the failure:** `findAnchorID` scanned a 256-byte window forward from each heading line, without respecting section boundaries. For input:

```
# Module          ← anchor finder runs from here

## Token Rotation
<!-- @id: token-rotation -->
```

The finder for `# Module` matched `<!-- @id: token-rotation -->` from the *next* section — the anchor was within 256 bytes. The level-1 `Module` section then got `AnchorID = "token-rotation"`. Since `Module` has no other level-1 heading after it, its `ByteEnd = len(src)` (it subsumes the entire document).

When the test then looked up `section_id = "token-rotation"`, `FindByID` returned `Module` instead of `Token Rotation`. The splice ran across the whole file, dropping the prefix `# Module\n\n` and the suffix beginning at `## Other`.

The bug is in our anchor parser, **not** in goldmark. Goldmark returned correct byte offsets for every heading. The byte-preserving splice itself works exactly as designed.

**Fix:** Tightened the anchor finder to a strict rule (now reflected in the pattern doc):

- Look only at the line immediately after the heading line.
- Allow at most one blank line between heading and anchor.
- Do NOT scan through intervening headings or any non-blank, non-anchor content.
- The closing `-->` must appear on the same line as the opening `<!-- @id:`.

Code: `findAnchorID` in `splice.go`. Diff is ~25 lines.

**Regression coverage added:**

- Three new unit cases in `TestAnchorIDExtraction`:
  - `does not cross next heading`
  - `does not cross with two blanks`
  - `does not cross intervening text`
- New fixture `09-multiple-anchors`: three anchored sections in one file; lookup `beta` must hit only the middle section.

### 2026-05-26 — Validated after fix

Re-run by user on Windows 10 + Go 1.22+. Result: **all green.**

- `TestFixtures` — 9/9 PASS (including new `09-multiple-anchors`).
- `TestAnchorIDExtraction` — 8/8 PASS (5 original + 3 regression cases for the cross-section anchor leak).
- `TestParseSectionsBasic` — PASS.
- `TestSpliceRangeValidation` — 3/3 PASS.

S1 closed. Approach is ready to be lifted into `internal/markdown/` during M1.

## Decision outcome

**GO** for the byte-preserving engine pattern, with the caveats above documented in [byte-preserving-engine.md](../patterns/byte-preserving-engine.md):

- Goldmark exposes byte offsets correctly for ATX headings.
- The section-end rule "next heading at same or higher level, or EOF" works as expected; nested headings behave correctly.
- Fenced code blocks containing `#` are correctly not treated as headings.
- YAML frontmatter, when parsed by goldmark default config, does not create heading entries that interfere with section parsing for our fixture style.

**Caveat that fed back into the design:** The `@id` anchor convention is "immediately after the heading line." The spike confirmed that anything looser (a search window) leaks across sections. This becomes a hard rule, not a guideline — both in the parser and in the auto-assignment writer (M1).

## Next steps after GO

1. Move spike code into `internal/markdown/` with any adjustments learned from validation.
2. Add `AssignMissingIDs` per design doc §12.5.
3. Add CRLF normalization helper.
4. Replicate fixtures under `internal/markdown/testdata/` and expand coverage (CRLF, BOM, edge cases discovered in validation).
5. Proceed to M0 bootstrap.

## Next steps if NO-GO on goldmark

1. Implement regex-based heading detector (`^#{1,6}\s`, with code-fence awareness via a simple state machine).
2. Splice algorithm doesn't change — only the offset source.
3. Document the fallback in the pattern doc.
4. Re-run all S1 fixtures against the alternative detector.
