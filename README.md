# agent-memory

Local context middleware for AI coding agents. One MCP call in, structured memory updates out. Branch-aware. Secret-safe. Byte-preserving. Three tools.

## Status

Early development. Currently in the spike phase for Release 0.1 (Core Contract Validation).

| Document | Purpose |
|---|---|
| [Design Doc v0.4.1](agent-memory-design-doc-v0.4.1.md) | Current canonical design. |
| [Implementation Plan](agent-memory-implementation-plan.md) | Release cuts (0.1 / 0.2 / 0.3) and milestone breakdown. |
| [Patterns](docs/patterns/) | Reusable design patterns documented as they are implemented. |
| [Spikes](docs/spikes/) | Pre-M1 spike outcome docs. |

Older revisions preserved for traceability:

- [Design Doc v0.4](agent-memory-design-doc-v0.4.md)
- [Design Doc v0.3](agent-memory-design-doc-v0.3.md)

## Build

Requires Go 1.22+.

```bash
go mod tidy
go test ./...
```

The byte-preserving Markdown engine spike (S1) lives in `spikes/s1-byte-preserving-markdown/` and runs via:

```bash
go test ./spikes/s1-byte-preserving-markdown/...
```

## Layout

```
cmd/agent-memory/                       # CLI entry point (M0+)
internal/                               # Core packages (M1+)
spikes/                                 # Pre-M1 spike investigations
  s1-byte-preserving-markdown/          # S1: AST locate + byte splice
docs/
  patterns/                             # Reusable design patterns
  spikes/                               # Spike outcome docs
agent-memory-design-doc-*.md            # Design history
agent-memory-implementation-plan.md     # Build plan
```

## License

TBD. See [implementation plan §18 Open Decisions](agent-memory-implementation-plan.md).
