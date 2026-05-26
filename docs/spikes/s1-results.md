# Spike S1 — Byte-Preserving Markdown Engine

**Status:** Code complete; awaiting empirical validation on a Go-equipped machine.
**Started:** 2026-05-26
**Goal:** Prove `yuin/goldmark` exposes byte offsets reliably enough to splice Markdown sections without round-tripping through the renderer.

## Decision: PENDING

Pending `go test ./spikes/s1-byte-preserving-markdown/...` results on a machine with Go 1.22+.

- **GO** if all 8 fixtures pass and byte-preservation invariants hold. The approach is validated and the M1 implementation can adopt it.
- **NO-GO on goldmark** if any fixture reveals a fundamental limitation. Fallback: regex-based heading detection (see [pattern doc Alternatives](../patterns/byte-preserving-engine.md)). The splice algorithm itself is unaffected; only the heading-offset source changes.

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

| # | Name | What it tests |
|---|---|---|
| 01 | `replace-section-simple` | Baseline: ATX h2 replacement by heading text. |
| 02 | `replace-by-id` | `<!-- @id: ... -->` anchor lookup. |
| 03 | `code-fence-with-hash` | `#` lines inside fenced code blocks are not parsed as headings. |
| 04 | `end-of-file-section` | Last section's `ByteEnd = len(src)`; trailing newline preserved. |
| 05 | `duplicate-headings` | Disambiguate via `occurrence` field. |
| 06 | `nested-headings` | Level-2 section subsumes level-3 children; `@id` lookup of parent. |
| 07 | `html-comment-before-heading` | Unrelated HTML comments don't confuse the anchor finder. |
| 08 | `yaml-frontmatter` | Frontmatter precedes first heading and is untouched by splice. |

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
- `@id` anchor finder uses bounded forward scan (256 bytes) after each heading line.

**Empirical validation not yet performed.** The development environment used to scaffold this spike does not have Go installed. The user (or any contributor) needs to install Go 1.22+ and run the test suite to confirm. See [How to validate](#how-to-validate).

### (Slot for next finding)

Once tests run, record:

- Pass/fail per fixture (paste `go test -v` output here).
- Any goldmark quirks discovered (e.g., heading byte offset surprises with specific input shapes).
- Any test that needed adjustment.
- Final GO / NO-GO with rationale.

## Decision outcome

(Pending validation. Will be filled in after `go test` runs.)

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
