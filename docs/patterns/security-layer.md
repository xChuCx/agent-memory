# Pattern: Security Layer (Secrets + Allowlist + Provenance)

**Status:** Implemented in [`internal/memory/secrets.go`](../../internal/memory/secrets.go), [`internal/memory/allowlist.go`](../../internal/memory/allowlist.go), [`internal/memory/provenance.go`](../../internal/memory/provenance.go).
**Owner:** `internal/memory/` (M3).
**Tracks design:** [Design Doc v0.4.1 §23](../../agent-memory-design-doc-v0.4.1.md).

## Problem

When an agent submits `propose_update`, three classes of unsafe content must be caught BEFORE the bytes touch disk:

1. **Credentials** — API keys, tokens, private keys. Once committed to memory they leak into git history, agent context, eval reports.
2. **Untrusted source attribution** — claims like "this fact comes from an external blog post" turning into durable decisions in `decisions.md`. Erodes provenance.
3. **Format violations of the security schema** — missing source citations on staged categories that require them.

The security layer runs as a phase of the orchestrator (T3.7) AFTER per-op `Validate()` and BEFORE writing. Any single failing check rejects the entire `propose_update` (no partial application).

## Solution

Three composable validators in `internal/memory/`. None of them perform I/O; each is a pure function over content + policy.

```
┌────────────────────────────────────────────────────────────────────┐
│  propose_update pipeline (orchestrator, T3.7)                       │
│                                                                     │
│  for each op:                                                       │
│    op.Validate(schema)            ← structural checks (M3 batch 1)  │
│                                                                     │
│  on the proposed POST-state bytes per file:                         │
│    allowlist := ExtractAllowlistRegions(content)                    │
│    findings  := Scan(content, ScanOpts{Allowlist: allowlist})       │
│    if findings → reject "secret_detected"                           │
│                                                                     │
│  for the proposal-level provenance:                                 │
│    violations := ValidateProvenance(category.Provenance, ctx)       │
│    if violations → reject "provenance_violation"                    │
│                                                                     │
│  ... apply or stage                                                 │
└────────────────────────────────────────────────────────────────────┘
```

## Secret scanner

```go
type Finding struct {
    Type                string  // "aws_access_key" | "github_token" | "jwt" | "high_entropy" | ...
    Line                int     // 1-based
    ApproximateLocation string  // "line N"
}

func Scan(content []byte, opts ScanOpts) []Finding
```

**The Finding intentionally never carries the matched bytes.** Per design doc §13.2 / §23.3, the scanner must not echo full secret values back to the caller or to logs. Type + line is enough for the agent to find and rewrite.

### Detection rules

Two layers:

1. **Regex set** — high-confidence patterns for well-known token shapes:
   - AWS access key (`AKIA`/`ASIA` + 16 chars)
   - GitHub token (`gh[p|o|u|s|r]_` + 36 chars)
   - GitLab PAT (`glpat-` + 20 chars)
   - Anthropic API key (`sk-ant-` + 40 chars)
   - OpenAI API key (`sk-` + 32 chars, optionally `sk-proj-`)
   - Stripe live key (`sk_live_` / `pk_live_` / `rk_live_` + 24 chars)
   - JWT (`eyJ` header + two `.`-separated base64-urlsafe segments)
   - PEM/SSH private key blocks (`-----BEGIN ... PRIVATE KEY-----`)

2. **Shannon-entropy** — catch-all for unknown-format secrets. Tokenises content on `[A-Za-z0-9_+/=-]+` runs; for each token >= `EntropyMinLength` characters, computes Shannon entropy and flags any that meets `EntropyThreshold`. Recommended thresholds per design doc §23.2: 4.5 bits/char over >= 32 chars.

Entropy hits are suppressed when the same line already has a higher-confidence regex finding — avoids double-reporting on JWTs and the like.

### Why both layers

Regex catches the canonical shapes deterministically and with low false-positive rates. Entropy catches everything else — private API keys, internal token formats, password hashes — at the cost of occasional false positives that the allowlist handles.

## Allowlist regions

```md
<!-- @secret-scan: allow reason="docs: AWS key format example" -->
GitHub tokens start with `ghp_` followed by 36 chars:
- `ghp_AaBbCcDdEeFfGgHhIiJjKkLlMmNnOoPpQqRr` (example, not a real token)
<!-- @secret-scan: end -->
```

```go
func ExtractAllowlistRegions(content []byte) ([]AllowlistRegion, error)
```

Rules:

- Markers are HTML comments matching `<!-- @secret-scan: allow reason="..." -->` and `<!-- @secret-scan: end -->`.
- `reason=` is mandatory and non-empty — post-mortem audits must know WHY the region was excluded.
- Markers must pair up: each open consumes the next end.
- Nesting is rejected (a second `allow` before the first `end` → parser error).
- Unmatched markers in either direction → parser error.
- The byte range a region covers starts AFTER the open marker's `>` and ends BEFORE the end marker's `<` — markers themselves are not inside the allowed range.

`IsAllowlisted(start, end, regions)` is a fast contains check the scanner uses on every regex hit.

### What the allowlist is NOT

- **Not a global disable.** There is no `disable_secret_scan` flag anywhere in the manifest by design (§23.7). The only way to bypass the scanner is per-region with an explicit reason.
- **Not an exception for sloppy commits.** The right use is documentation that explains a token format ("GitHub tokens start with ghp_..."), not "the scanner doesn't like my real credential". If you're allowlisting a real key, you are doing it wrong.

## Provenance validator

```go
type Source struct {
    Type string  // file | test | user | session | inference | external
    Ref  string  // file path, test name, etc.
}

type ProvenanceContext struct {
    Sources       []Source
    Confidence    string  // confirmed | inferred | user-provided | stale | unknown | ""
    IsNewSection  bool
}

func ValidateProvenance(policy schema.Provenance, ctx ProvenanceContext) []string
```

The category's `schema.Provenance` policy declares:

- `Required`               — sources non-empty on every operation.
- `RequiredForNewSections` — sources non-empty when the op creates a new section.
- `AllowedSourceTypes`     — if set, every source's `Type` must be in this list.
- `ForbiddenSourceTypes`   — no source's `Type` may be in this list.

The validator returns human-readable violation strings; the orchestrator surfaces these to the agent as a structured rejection.

### Why provenance lives here, not in `schema.Validate`

`schema.ValidateSection` (M3 batch 1) checks per-section *field structure*: Date format, Status enum, etc. — declared by `SectionSchema`. Provenance is a separate concern (declared by `Provenance`) and operates on operation-level metadata (the `sources` field of the `propose_update` input), not the section body. Keeping the two split lets each one be tested in isolation and keeps the failure messages distinct.

## What never gets logged

Cross-cutting rule for the whole layer: **never log the matched secret bytes.**

- `Finding.Type` is a constant string ("github_token" etc.) — safe.
- `Finding.ApproximateLocation` is "line N" — safe.
- The matched bytes never leave `Scan`'s stack frame.

The orchestrator's logging path enforces the same — `slog` calls only reference Finding fields, never re-slice the original content using the finding's offsets.

## References

- [Design Doc v0.4.1 §13.2 / §23](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.2 T3.4 / T3.5 / T3.6](../../agent-memory-implementation-plan.md).
- OWASP Secrets-in-Source — broader threat model.
