# Pattern: Structured Logging (slog)

**Status:** Implemented in [`internal/logging/logging.go`](../../internal/logging/logging.go); wired through [`internal/cli`](../../internal/cli), [`internal/mcp`](../../internal/mcp), and the [`internal/memory`](../../internal/memory) deps.
**Owner:** `internal/logging/` (observability, M-series polish).
**Tracks design:** [Design Doc v0.4.1 §13.2 / §23.3](../../agent-memory-design-doc-v0.4.1.md) (secret-safety), operational observability.

## Problem

The binary needs operational observability — *did that proposal apply or
stage? why was it rejected? how big was the served pack?* — without
violating two hard constraints:

1. **stdout is a protocol/data channel, not a log sink.**
   - `agent-memory mcp` speaks JSON-RPC over **stdout**. A single stray
     log line there corrupts the framing and the client disconnects.
   - CLI commands reserve **stdout** for their own output: the Markdown
     pack from `fetch`, the `--json` payloads, the human reports from
     `apply` / `rebase` / `status`. Logs interleaved with that output
     would break `| jq` pipelines and scripted consumers.

2. **Logs must never echo secret or PII bytes.** Memory content can
   contain credentials a caller tried to write; the secret scanner
   rejects them, but the *rejection log* must not re-leak them
   (§13.2 / §23.3). A log file is just another place a credential can
   end up in git history or an eval report.

## Solution

A tiny `internal/logging` package that makes the safe thing the only
easy thing: every logger is built **to an explicit `io.Writer`** (always
`os.Stderr` in production) and is **quiet by default** (WARN).

```go
const EnvLevel = "AGENT_MEMORY_LOG" // debug | info | warn | error

func New(w io.Writer, level slog.Level) *slog.Logger // text handler → w
func Nop() *slog.Logger                              // slog.DiscardHandler
func ParseLevel(name string, fallback slog.Level) slog.Level
func LevelFromEnv(fallback slog.Level) slog.Level    // reads $AGENT_MEMORY_LOG
func FromEnv(w io.Writer) *slog.Logger               // New(w, env-or-WARN)
```

There is no `New()` that defaults the writer to stdout, and no
package-level "default logger" tied to stdout. The caller picks the
writer deliberately — that's the forcing function for rule #1.

```
┌──────────────────────────────────────────────────────────────────────┐
│  stdout                          │  stderr                            │
│  ───────                         │  ───────                           │
│  MCP: JSON-RPC frames            │  ALL logs (every transport)        │
│  CLI: Markdown pack / --json /   │  built via logging.New(os.Stderr…) │
│       human reports              │  quiet by default (WARN)           │
└──────────────────────────────────────────────────────────────────────┘
```

## Where loggers come from

| Surface | Construction | Level source |
|---|---|---|
| CLI | `cliLogger()` in [`cli/root.go`](../../internal/cli/root.go) → `logging.New(os.Stderr, …)` | `--log-level` flag → `$AGENT_MEMORY_LOG` → WARN |
| MCP server | `logging.FromEnv(os.Stderr)` in [`mcp.New`](../../internal/mcp/server.go) | `$AGENT_MEMORY_LOG` → WARN |
| Tests | `nil` deps logger, or a buffer logger | n/a (nil → no-op) |

The CLI exposes a persistent `--log-level debug|info|warn|error` flag;
the MCP server (no flag surface) reads `$AGENT_MEMORY_LOG`. Both end up
on stderr.

## Threading through deps (optional, nil-safe)

`UpdateDeps` and `FetchDeps` carry an **optional** `Logger *slog.Logger`.
Call sites never nil-check — they go through a helper that falls back to
a shared discard logger:

```go
var nopLogger = logging.Nop() // package var; DiscardHandler is stateless

func (d UpdateDeps) log() *slog.Logger {
    if d.Logger != nil {
        return d.Logger
    }
    return nopLogger
}
```

So a test that builds `UpdateDeps{...}` without a `Logger` is silent, and
production wires the real stderr logger in. `StatusDeps` deliberately has
**no** `Logger`: `memory.status` is read-only and quiet by contract.

## What gets logged

The instrumented mutation/read entry points log at most twice: an entry
line and a **single deferred terminal-outcome line** that covers every
return path. This avoids sprinkling `log.X` across ~15 reject sites.

| Function | Entry | Terminal outcome |
|---|---|---|
| `ProposeUpdate` | `Debug "propose_update received"` (intent, op count) | applied/staged → `Info`; rejected → `Info`, **WARN if `secret_detected`/`pii_detected`** |
| `ApplyStaged` | — | applied → `Info` (file count); rejected → `Info` (reason, drift count) |
| `RebaseStaged` | — | rebased → `Info`; skipped-clean → `Debug`; rejected → `Info`, **WARN if secret/PII** |
| `BuildContextPack` | — | served → `Debug` (mode, included/omitted counts, budget) |

Leveling rule: a credential/PII rejection is the one outcome worth a
human's attention on an otherwise-quiet WARN console, so it logs at WARN.
Everything else is INFO/DEBUG and silent by default.

## Secret-safety: what is NEVER logged

This is the part the design doc actually mandates, enforced by
construction **and** by test:

- **No matched secret/PII bytes.** Terminal logs reference
  `len(resp.Findings)` (a count) and `resp.Reason` (a stable code like
  `secret_detected`), never the `Finding` itself — and `Finding` carries
  no bytes to begin with (see [security-layer.md](./security-layer.md)).
  Content is never re-sliced by a finding's offset for a log message.
- **No raw fetch query.** `BuildContextPack`'s served-summary log omits
  `req.Query` on purpose: a query is agent-controlled free text and could
  itself echo a secret the caller is searching for. Mode + counts +
  budget only.

[`internal/memory/redaction_test.go`](../../internal/memory/redaction_test.go)
captures every emitted record (at Debug, so nothing is filtered) into a
buffer and asserts:

- the terminal log *did* fire (no vacuous pass) and recorded the safe
  reason code, **and**
- the credential (`AKIA…`), the PII digits, and the raw query are all
  absent from the captured output.

## Configuration

```sh
# quiet (default): only WARN+ — credential/PII rejections, real problems
agent-memory apply --latest

# see routing + outcomes
AGENT_MEMORY_LOG=info agent-memory apply --latest
agent-memory --log-level=info apply --latest   # CLI flag, same effect

# full detail (entry lines, fetch summaries)
AGENT_MEMORY_LOG=debug agent-memory fetch "token rotation"
```

For the MCP server the client sets the env var when spawning the process;
all output lands on the server's stderr, leaving the stdout JSON-RPC
channel pristine (verified by the e2e smoke test, which parses stdout).

## Why not log to a file / structured JSON / a logging framework

- **Text handler, not JSON.** The audience is a human tailing stderr or a
  client surfacing server stderr. `slog`'s `TextHandler` is greppable and
  dependency-free. (A JSON handler is a one-line swap in `logging.New` if
  a consumer ever needs it.)
- **No log files.** The binary writes exactly one tree it owns —
  `.agent-memory/` — and logs are ephemeral diagnostics, not durable
  state. Persisting them would be another thing to rotate, secure, and
  keep secrets out of.
- **stdlib `slog`, no framework.** Go 1.24+ ships
  `slog.DiscardHandler`; the whole package is ~80 lines. Matches the
  project's CGo-free, few-dependencies posture.

## References

- [Design Doc v0.4.1 §13.2 / §23.3](../../agent-memory-design-doc-v0.4.1.md) — secret-safety in logs.
- [security-layer.md](./security-layer.md) — the `Finding` type that carries no bytes.
- [mcp-tool-server.md](./mcp-tool-server.md) — why stdout is sacred for JSON-RPC.
- Go [`log/slog`](https://pkg.go.dev/log/slog) — the structured logging stdlib.
