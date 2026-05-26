# Pattern: Byte-Preserving Markdown Engine

**Status:** Implemented in [`internal/markdown/`](../../internal/markdown). Spike-validated in S1; production tests exercise the same fixture corpus.
**Owner:** `internal/markdown/` (M1).
**Tracks design:** [Design Doc v0.4.1 §21](../../agent-memory-design-doc-v0.4.1.md).

## Problem

Markdown libraries (including `yuin/goldmark`) parse text into an AST. Rendering that AST back to text is **not byte-identical** to the input:

- List markers may normalize (`*` → `-`).
- Code fence styles may normalize (backticks ↔ tildes).
- Indentation may regularize.
- Blank line counts may shift.
- Trailing whitespace may be stripped.

Every AST round-trip therefore produces a diff much larger than the actual semantic edit, breaks `git blame` for unchanged lines, and creates merge conflicts where none should exist.

We need to edit Markdown files in a way that **leaves unchanged regions byte-identical** to the input.

## Solution

Use the AST only to **locate** byte ranges. Never use the AST to **produce** output.

```
src bytes  ──parse──▶  AST
                        │
                        │ locate target section's byte range
                        ▼
src bytes  ──splice──▶  new bytes
            [start, end) replaced with caller-supplied content
```

The renderer is never invoked. Output bytes are produced by string-level splice on the original source.

### Algorithm

1. Parse `src` bytes into a goldmark AST.
2. Walk the AST. For each `*ast.Heading` node:
   - Extract the plain heading text via inner `*ast.Text` / `*ast.String` walks.
   - Get the heading content's first segment via `node.Lines().At(0).Start` — this points after the `## ` marker prefix.
   - Walk backward from that position to the previous `\n` (or start-of-file) to get the **heading line start byte**.
3. For each heading, compute its **section end byte**: the heading line start of the next heading at the same or higher level (smaller level number), or `len(src)` if none.
4. Resolve `@id` anchors with a **strict positional rule**, not a window:
   - The anchor must be on the line immediately after the heading line, or after at most one blank line.
   - The opening `<!-- @id:` and the closing `-->` must be on the same line.
   - Do not scan through intervening headings or any non-blank, non-anchor content. A loose window leaks anchors across section boundaries — confirmed by spike S1 fixture `02-replace-by-id`.
5. To replace a section:

   ```
   out = src[:ByteStart] + replacement + src[ByteEnd:]
   ```

### Properties guaranteed

- Bytes in `src[:ByteStart]` are unchanged in output.
- Bytes in `src[ByteEnd:]` are unchanged in output.
- `git blame` is preserved for unchanged lines.
- No whitespace, line-ending, or marker normalization happens to unchanged regions.
- The replacement bytes are caller-supplied; the engine does not generate them.

### Caller responsibilities

- The replacement string must include the heading line if the original had one (for `replace_section`).
- The replacement string should respect the file's line-ending convention (LF vs CRLF) — engine doesn't normalize.
- For files with an `@id` anchor, the replacement should preserve the anchor line. The engine doesn't enforce this in M1; schema validation enforces it from M3 onward.

## Edge cases handled

| Case | Behavior |
|---|---|
| Fenced code blocks with `#` lines | Goldmark doesn't return them as headings; section-boundary detection naturally ignores them. |
| End-of-file section | `ByteEnd = len(src)`. Trailing newline is preserved by the prefix/suffix splice. |
| Duplicate heading text | Each section has an `Occurrence` counter (1-based). Callers disambiguate via `(text, level, occurrence)` or via the section's `@id` anchor. |
| Nested headings | A level-N section ends at the next heading of level ≤ N. So a level-1 section subsumes child level-2+ sections — caller picks the level appropriate to their intent. |
| Unrelated HTML comments | The anchor finder only inspects the line immediately following the heading (optionally with one blank line of slack), and only matches the specific `<!-- @id: ... -->` pattern. Other HTML comments anywhere else are content. |
| YAML frontmatter | Goldmark parses `---` as a thematic break; frontmatter content becomes a paragraph before the first heading. The section parser only operates from the first heading onward; frontmatter is in the splice prefix and is untouched. |

## Edge cases deferred

| Case | Reason |
|---|---|
| CRLF line endings | Splice itself preserves CRLF bytes outside the replacement; replacement content must match. Detection + normalization helper is M1 work. |
| BOM | The 3-byte UTF-8 BOM is part of `src[:ByteStart]` for any section that follows it. Preserved trivially by splice — testing it requires fixture-write tooling that doesn't auto-convert on Windows. |
| Setext heading style (underlined) | Out of scope per design doc; M1 only emits ATX. |
| Inline HTML in heading text | Out of scope for MVP. Heading-text lookup may miss such cases, but `@id` anchor lookup is unaffected. |

## Alternatives considered

### AST round-trip render

Parse → mutate AST → render. **Rejected** because `goldmark`'s renderer is not byte-faithful for arbitrary input. Even the `goldmark/renderer/markdown` package (community-maintained) makes opinionated formatting choices that produce noisy diffs.

### Regex-based heading detection

Find headings via `^#{1,6}\s` against raw bytes, with hand-rolled code-fence skipping. **Rejected as primary approach** because correctly handling all fenced code block forms (backtick, tilde, indented, fences inside lists) is fiddly and goldmark already does it.

Kept as **documented fallback** if goldmark proves insufficient on any future fixture. The splice algorithm itself is independent of how heading byte offsets are obtained, so swapping the detector is local.

### Heading anchor replacement via AST mutation

Mutate the AST in place and rely on goldmark to render only the changed subtree. **Rejected** because the renderer can still reformat surrounding content; we don't trust it.

## API (package `internal/markdown/`)

```go
type Section struct {
    HeadingText  string
    HeadingLevel int
    AnchorID     string // empty if no @id anchor
    Occurrence   int    // 1-based among same (text, level) sections
    ByteStart    int    // inclusive; start of heading line
    ByteEnd      int    // exclusive
    ContentHash  string // "sha256:<hex>" of bytes [ByteStart, ByteEnd)
}

func ParseSections(src []byte) ([]Section, error)
func FindByID(sections []Section, id string) (*Section, bool)
func FindByHeading(sections []Section, heading string, level, occurrence int) (*Section, bool)

// Multi-op byte splice. All offsets refer to the ORIGINAL src; Splice sorts
// internally and stitches the result. Overlapping ops error out.
type SpliceOp struct {
    ByteStart   int
    ByteEnd     int
    Replacement []byte
}

func Splice(src []byte, ops []SpliceOp) ([]byte, error)
func ReplaceSection(src []byte, sec Section, newContent []byte) ([]byte, error)

// Post-splice sanity check (goldmark parses cleanly) and a small helper.
func ValidateMarkdown(src []byte) error
func CountSections(src []byte) (int, error)
```

Still to come (M1 follow-up):

- `AssignMissingIDs(src) (newSrc, assigned, err)` — T1.5. Auto-inserts `<!-- @id: ... -->` anchors after headings that don't have one. Required so the production engine can ingest legacy files written without anchors.
- CRLF normalization helper — out of scope for the spike's fixture coverage; needed once we ingest real cross-platform repos.

### Differences from the spike

| Aspect | Spike S1 | M1 production |
|---|---|---|
| Package | `package s1` | `package markdown` |
| `Section.ContentHash` | absent | sha256 of section bytes |
| `Find*` return | `(Section, bool)` | `(*Section, bool)` |
| Splice API | single `Splice(src, start, end, repl)` + `ReplaceSection` | multi-op `Splice(src, []SpliceOp)`; `ReplaceSection` now wraps `Splice` |
| Test corpus location | `spikes/s1-byte-preserving-markdown/testdata/` (historical) | `internal/markdown/testdata/` (canonical) |

## References

- [Design Doc v0.4.1 §21](../../agent-memory-design-doc-v0.4.1.md) — engine specification.
- [Design Doc v0.4.1 §12](../../agent-memory-design-doc-v0.4.1.md) — section identity model.
- [Spike S1 Results](../spikes/s1-results.md) — empirical validation status.
- [Implementation Plan §3 S1](../../agent-memory-implementation-plan.md).
