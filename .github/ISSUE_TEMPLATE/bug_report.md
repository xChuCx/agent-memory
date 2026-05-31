---
name: Bug report
about: Something doesn't work as expected
title: "bug: "
labels: bug
---

**What happened**
A clear description of the bug and what you expected instead.

**Repro steps**
1. ...
2. ...

**Version & environment**
- `agent-memory version`:
- OS / arch:
- How it's invoked (CLI command, or MCP via which host agent):

**Logs**
Re-run with `AGENT_MEMORY_LOG=debug` (logs go to stderr) and paste the
relevant lines. **Do not paste real secrets** — redact them.

**Notes**
Anything else (a minimal `.agent-memory/` snippet, the failing query, etc.).
For security-sensitive issues, use [SECURITY.md](../SECURITY.md) instead of
a public issue.
