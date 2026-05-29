# Module: internal/memory
<!-- @id: module-memory -->

The core orchestrator and domain logic. Everything that mutates or reads
the `.agent-memory/` store lives here. Pure Go; no MCP/CLI coupling.

## propose_update pipeline
<!-- @id: memory-propose-pipeline -->

`ProposeUpdate(ctx, req, deps) -> ProposeResponse` (`update.go`) runs the
write pipeline: validate intent + ops, parse/plan each operation, splice
bytes in-memory, validate resulting Markdown + section schema (affected
sections only), secret scan + allowlist, PII scan, provenance check, then
route apply-vs-stage. Applied writes are atomic + re-indexed + trigger
`index.md` regeneration; staged writes land under `staging/<id>/`.
Returns rejections in the response (not Go errors) so the MCP wrapper
stays simple. Single deferred terminal-outcome log per call.
**Sources:** internal/memory/update.go, internal/memory/routing.go

## staging lifecycle
<!-- @id: memory-staging -->

`staging.go` + `rebase.go` + `sweep.go` + `rejection_log.go`. Staging IDs
are `<UTC YYYYMMDDTHHMMSS>-<slug>`. `ApplyStaged` re-checks drift against
recorded target checksums before writing; `RebaseStaged` re-plans soft
drift (with `--force`); `SweepStale` GC's expired staging dirs;
`ResolveStagingID` accepts a full id, unique prefix, or `--latest`.
Drift policies: RequireSectionContentMatch / RequireSectionResolvable /
RequireFileAbsent / RequireFilePresent.
**Sources:** internal/memory/staging.go, internal/memory/rebase.go

## operations and the byte-splice
<!-- @id: memory-operations -->

`operations.go` implements 8 ops: create_file, replace_section,
append_section, append_to_section, replace_section_content (MVP 5) +
archive_section, remove_section, rename_heading (M4). Each has
Plan/Validate; multi-file ops implement `ExtraFileProducer`. Archive
files are write-once.
**Sources:** internal/memory/operations.go

## security layer
<!-- @id: memory-security -->

`secrets.go` (regex + Shannon entropy), `pii.go` (patterns + Luhn),
`allowlist.go` (per-region `<!-- @secret-scan: allow reason=... -->`,
no global disable), `provenance.go` (source-type policy). `Finding`
carries Type + line only — never the matched bytes.
**Sources:** internal/memory/secrets.go, docs/patterns/security-layer.md

## fetch + dedup + index regeneration
<!-- @id: memory-fetch-index -->

`fetch.go` assembles the budgeted context pack (bootstrap vs search);
`jaccard.go` suppresses near-duplicate sections (token Jaccard > 0.85)
before budget; `index_gen.go` deterministically regenerates `index.md`;
`status.go` builds the health report.
**Sources:** internal/memory/fetch.go, internal/memory/jaccard.go
