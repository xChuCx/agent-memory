# Pattern: Security Hardening — Allowlist Limits + PII Detection

**Status:** Implemented in [`internal/memory/pii.go`](../../internal/memory/pii.go), [`internal/memory/allowlist.go`](../../internal/memory/allowlist.go), [`internal/memory/secrets.go`](../../internal/memory/secrets.go).
**Owner:** `internal/memory/` (Release 0.3).
**Builds on:** [Pattern: Security Layer](security-layer.md).

## Problem

v0.1.0's security layer caught credentials via regex + Shannon-entropy,
with allowlist regions as a per-region escape hatch. Two gaps remained:

1. **Allowlist regions had no size cap.** A careless or malicious
   agent could wrap a 5 KB region around real credentials with a
   plausible-looking reason, bypassing the scanner entirely. The
   allowlist's intended use ("here's what a token format looks
   like") fits in ~100-200 bytes; nothing in the original design
   stopped abuse.
2. **Only credentials, not PII.** SSN, credit-card, and email
   leaks are durable identifiers in the same risk class as
   credentials. A scanner that catches AWS keys but waves through
   `Customer SSN: 123-45-6789` is an incomplete defence.

This hardening kit closes both.

## Solution: allowlist limits

`manifest.security.allowlist_limits` caps three dimensions:

```yaml
security:
  allowlist_limits:
    max_bytes_per_file:   1024   # sum across all regions
    max_regions_per_file: 10     # count
    max_bytes_per_region: 512    # single largest region
```

A field set to `0` disables the corresponding check (escape hatch
for projects with legitimate need — rare). DefaultManifest sets the
values above.

`CheckAllowlistLimits(regions, limits)` is the pure function the
orchestrator uses. Returns an empty string on pass, a human-readable
diagnostic on breach. Checks in this order:

1. **Region count** — `regions = N, max allowed = M`
2. **Single region size** — `region[i] size = N bytes, max allowed = M (reason="...")`
3. **Total bytes across all regions** — `total = N bytes across K region(s), max allowed = M`

The region-count check wins first when multiple limits would fire,
because count is the cheapest to diagnose ("you have too many
escape hatches" is more actionable than the byte math).

### Why these numbers

- **1024 bytes total per file**: enough for ~5 token-format
  examples (200 bytes each) plus surrounding prose. Real-world
  legitimate use stays under this comfortably.
- **10 regions per file**: more regions usually signals
  over-escaping (the user is fighting the scanner rather than
  rewriting the content).
- **512 bytes per region**: roughly the upper bound of a single
  token-format example with explanation. A single region this
  large is plausible but conservative.

All three are adjustable per project. A docs-heavy repo that
extensively documents many token formats can raise the limits in
their `meta/manifest.yaml`.

## Solution: PII patterns

`internal/memory/pii.go` adds three new `Finding.Type` values, all
prefixed `pii_`:

| Type | Pattern | Confirmation |
|------|---------|--------------|
| `pii_ssn` | `\b\d{3}-\d{2}-\d{4}\b` | (none — shape is rare) |
| `pii_credit_card` | `\b(?:\d[ \-]?){13,19}\b` | **Luhn algorithm** |
| `pii_email` | RFC-ish `<user>@<domain>.<tld>` | (none) |

`ScanOpts` controls them:

```go
opts.PIIScanSSNAndCC = true   // default ON via manifest.security.pii_scan
opts.PIIScanEmail    = true   // default OFF; opt-in via pii_scan_email
```

### Why the Luhn gate

Without the Luhn check, every 13+ digit run would false-positive:
long IDs (`1234567890123456`), hash fragments (`abc123...`),
timestamps with no spaces. The Luhn algorithm filters those out —
real credit card numbers are Luhn-valid by construction, random
digits have ~10% chance of accidentally being Luhn-valid (a 90%
false-positive cut).

Stripe's documented test numbers (`4242424242424242` etc.) are
Luhn-valid by design; they fire the detector correctly. Random
16-digit IDs in code or fixtures don't.

### Why email is opt-in

Emails appear in legitimate documentation: maintainer addresses
(`mailto:` in headers), example syntax (`user@example.com`),
support contacts. Default-on detection would force users to
allowlist every example. Default-off respects the typical
documentation pattern; the rare project that needs to detect
emails (financial / medical compliance contexts) flips the manifest
flag.

## Solution: classification

When `Scan` returns a mix of credential AND PII findings, the
orchestrator needs to pick ONE reject reason. `ClassifyFindings`
applies "most severe wins":

```go
// pseudocode
if any finding's Type doesn't start with "pii_" → "secret_detected"
else                                            → "pii_detected"
```

The full Findings slice (both kinds) is still returned in the
response, so the agent sees everything. Just the headline reason
code differs.

Same rule applies to the rebase path: `ReasonRebaseSecret` /
`ReasonRebasePIIDetected` mirror the orchestrator's split.

## Integration points

The orchestrator (`internal/memory/update.go`) and the rebase path
(`internal/memory/rebase.go`) both run the hardened scan:

```
ExtractAllowlistRegions(content)
   ↓
CheckAllowlistLimits(regions, limits)        ← new reject: allowlist_limit_exceeded
   ↓
Scan(content, opts{                          ← PII walks after secret regex
    Allowlist:        regions,
    PIIScanSSNAndCC:  manifest.security.pii_scan,
    PIIScanEmail:     manifest.security.pii_scan_email,
})
   ↓
ClassifyFindings(findings)                   ← new reject: pii_detected
                                               (existing: secret_detected wins on mix)
```

Both phases respect the cross-process advisory lock the orchestrator
already holds.

## What this hardening does NOT do

- **No machine-learning PII detection.** Regex + Luhn are deterministic
  and fast. Detecting addresses, names, ages, etc. needs ML (or at
  least a large name database). Out of scope for v0.3.
- **No DLP-style risk scoring.** Every finding is a hard reject;
  there's no "low confidence — log but allow". The threat model
  assumes a careful agent that can rewrite when challenged, not a
  user-trust gradient.
- **No allowlist signature / authentication.** Anyone with write
  access to the source file can add an allowlist marker. The cap
  is on size, not on who's allowed to use them.
- **No PII redaction.** When the scanner rejects, the agent must
  rewrite the content without the PII. We don't auto-redact (that
  would silently change agent intent).

## Backward compatibility

- Existing v0.2.0 / v0.1.0 manifests without
  `security.allowlist_limits` AND without `security.pii_scan`
  keep working: yaml.v3 leaves zero values, the orchestrator
  reads zero-limit fields as "disabled".
- A fresh `agent-memory init` (post-hardening) ships the new
  defaults — old behaviour is opt-out via manifest edit.
- ProposeUpdate's wire shape gains two new reason codes
  (`allowlist_limit_exceeded`, `pii_detected`). Existing agents
  that match against `secret_detected` see no change for
  credential rejections; they DO see the new codes when their
  content trips the new checks. Document this in adapter SKILLs.

## References

- [Pattern: Security Layer](security-layer.md) — the v0.1.0 base
  this hardens.
- [Design Doc v0.4.1 §23](../../agent-memory-design-doc-v0.4.1.md).
- [Implementation Plan §7.x hardening tasks](../../agent-memory-implementation-plan.md).
- Luhn algorithm — https://en.wikipedia.org/wiki/Luhn_algorithm.
