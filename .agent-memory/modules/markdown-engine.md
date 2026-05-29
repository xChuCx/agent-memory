# Module: internal/markdown
<!-- @id: module-markdown -->

The byte-preserving Markdown engine. Locates sections via the goldmark AST
and edits by splicing bytes — untouched content is never reflowed.

## section parsing + identity
<!-- @id: markdown-sections -->

`ParseSections(src) -> []Section` returns each heading's byte range
(ByteStart/ByteEnd), level, title, `@id` anchor, content hash, and a
1-based Occurrence counter for duplicate heading text. `FindByID` resolves
a section for editing. Section identity is `@id` first, then
(text, level, occurrence). Spike S1 validated round-trip byte preservation
across edge cases (duplicate headings, code fences, etc.).
**Sources:** internal/markdown, spikes/s1-byte-preserving-markdown

## why byte-splice, not render
<!-- @id: markdown-why-splice -->

Rendering Markdown back from an AST reflows whitespace, list markers, and
wrapping, producing huge spurious diffs and breaking git blame. The engine
computes the exact byte window for a section and replaces only those bytes,
so a single-section edit touches only that section's lines.
**Sources:** docs/patterns/byte-preserving-engine.md
