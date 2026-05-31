# Security Policy

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue
for anything exploitable.

- Preferred: GitHub → **Security** tab → **Report a vulnerability**
  (private advisory). This keeps the report confidential until a fix ships.
- If that is unavailable, open a minimal public issue titled
  "security: please contact" with no details, and a maintainer will set up
  a private channel.

When reporting, include: affected version (`agent-memory version`), repro
steps, and the impact you observed. We aim to acknowledge within a few days
and to coordinate a fix and disclosure timeline with you.

## Supported versions

This project is pre-1.0 and ships from `main`. Security fixes target the
latest tagged release and `main`. Older `0.x` tags are not back-patched —
upgrade to the newest release.

## Threat model & scope

agent-memory is a **local** tool: it reads and writes a `.agent-memory/`
directory in your repository and speaks MCP over stdio. It does not run a
network service, and durable memory is plain Markdown committed to your
git history.

In scope (please report):

- Path traversal or writes escaping `.agent-memory/`.
- A `propose_update` operation that bypasses validation, the secret/PII
  scanner, or the staging/approval gate for a category configured to stage.
- The MCP server emitting non-protocol bytes on **stdout** (corrupts the
  JSON-RPC channel) or leaking secret/PII bytes into logs.
- Advisory-lock bypass enabling concurrent writers to corrupt the store.

Out of scope / known limitations:

- **The secret and PII scanners are best-effort, not a guarantee.** They
  use regex + entropy + Luhn heuristics and will miss novel formats. Treat
  them as a safety net, not a control — never deliberately put real
  credentials in memory. The per-region allowlist (`@secret-scan: allow
  reason="…"`) is for documenting token *formats*, not for smuggling real
  secrets; there is intentionally no global disable.
- Test fixtures and documentation contain **fake**, credential-shaped
  strings (e.g. `ghp_…example`) by design, to exercise the scanner. These
  are not live secrets.
- Anything an agent or user with write access to the repository could already
  do directly to the files on disk.

See [docs/patterns/security-layer.md](docs/patterns/security-layer.md) for
the design of the scanning/allowlist/provenance layer.
