# Pattern: MCP Tool Server

**Status:** Partially implemented in [`internal/mcp/`](../../internal/mcp). `memory.fetch_context` is live as of M2 (T2.10). `memory.propose_update` and `memory.status` land in M3.
**Owner:** `internal/mcp/` (M2+).
**Tracks design:** [Design Doc v0.4.1 §14, §15](../../agent-memory-design-doc-v0.4.1.md).

## Problem

The product's external interface is an MCP server exposing exactly three tools:

- `memory.fetch_context` — return a ranked, budgeted context pack.
- `memory.propose_update` — accept structured Markdown update operations.
- `memory.status` — report memory health and metadata.

Each tool has a strict JSON I/O contract (design doc §15). We need:

- An SDK that supports stdio JSON-RPC with structured tool I/O.
- A tool declaration style that drives JSON Schema from Go types.
- A clean server lifecycle (init, run, graceful shutdown on stdin EOF).
- An error model where business-logic rejections (drift, secrets, schema violations) are **structured success responses**, not raw Go errors — matching the design's `status: "rejected"` shape.

## Solution

Use the official `github.com/modelcontextprotocol/go-sdk/mcp` package. Tool registration uses generics; the SDK derives the JSON schema from struct tags on the input/output types.

### Tool handler signature

```go
func Handler(ctx context.Context, req *mcp.CallToolRequest, input InputT) (
    *mcp.CallToolResult,
    OutputT,
    error,
)
```

Return semantics:

- **Normal result:** `return nil, output, nil`. The SDK serializes `OutputT` as the structured result.
- **Custom content blocks:** `return &mcp.CallToolResult{...}, output, nil`. For when we need to emit a content block alongside the structured result (probably not needed for our three tools).
- **Hard error:** `return nil, _, err`. The SDK wraps as an MCP Tool Error.

### Input/output schemas via struct tags

```go
type FetchContextInput struct {
    Query  string   `json:"query,omitempty" jsonschema:"search query; empty returns bootstrap context"`
    Scope  []string `json:"scope,omitempty" jsonschema:"optional paths or module names to prioritize"`
    Budget int      `json:"budget,omitempty" jsonschema:"approximate character budget"`
}
```

The SDK reads both `json:` (field name + omission rules) and `jsonschema:` (description, constraints).

### Server bootstrap

```go
server := mcp.NewServer(&mcp.Implementation{
    Name:    "agent-memory",
    Version: "0.4.1",
}, nil)

mcp.AddTool(server, &mcp.Tool{
    Name:        "memory.fetch_context",
    Description: "Return a budgeted, ranked context pack assembled from the project's .agent-memory/ files.",
}, fetchContextHandler)

mcp.AddTool(server, &mcp.Tool{
    Name:        "memory.propose_update",
    Description: "Accept structured memory update operations; route to apply or stage per category policy.",
}, proposeUpdateHandler)

mcp.AddTool(server, &mcp.Tool{
    Name:        "memory.status",
    Description: "Return memory health, freshness, branch, and staged-update metadata.",
}, statusHandler)

if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
    log.Fatal(err)
}
```

### Error model mapping

The design specifies structured responses with a `status` field (`applied` / `staged` / `rejected`). We map internal errors to outputs, not to Go-level errors over the wire:

| Internal condition | MCP response |
|---|---|
| `ErrSchemaViolation`, `ErrSecretDetected`, `ErrLockHeld`, `ErrTargetDrift`, `ErrAmbiguousSection`, `ErrInvalidPath`, etc. | Return `Output{Status: "rejected", Reason: ..., Findings: ...}` with `nil, output, nil`. |
| True system errors (disk failure, panicked goroutine recovery, OS-level lock failures) | Return `nil, _, err` so the SDK wraps as an MCP Tool Error. |

**Rule of thumb:** business-logic rejections are success responses with `status: "rejected"`. Only true infrastructure failures escalate to Go-level errors.

### Lifecycle and stdio hygiene

- `server.Run` blocks until the transport closes (stdin EOF when Claude Code shuts the server down).
- Use a parent context tied to `os.Interrupt` for graceful shutdown if the binary is run directly.
- **Stdout is the JSON-RPC frame channel — never write logs there.** All logging goes to stderr via `log/slog` or stdlib `log`.
- Long-running operations (especially `propose_update` with disk I/O + indexing) must respect `ctx.Done()`.

## Schema sketches for the three real tools

Stubs to validate the SDK can express our shapes. Finalized in M2/M3. These map 1:1 to the contracts in design doc §15.

### memory.fetch_context

```go
type FetchContextInput struct {
    Query          string   `json:"query,omitempty" jsonschema:"search query; empty returns bootstrap pack"`
    Scope          []string `json:"scope,omitempty" jsonschema:"paths or module names to prioritize"`
    Budget         int      `json:"budget,omitempty" jsonschema:"approximate character budget"`
    Include        []string `json:"include,omitempty" jsonschema:"context categories to include"`
    ExcludeArchive bool     `json:"exclude_archive,omitempty" jsonschema:"if true, archived files only returned on strong relevance match"`
}

type IncludedFile struct {
    Path         string `json:"path"`
    Reason       string `json:"reason"`
    Freshness    string `json:"freshness,omitempty"`
    Confidence   string `json:"confidence,omitempty"`
    SectionCount int    `json:"section_count,omitempty"`
}

type OmittedFile struct {
    Path   string `json:"path"`
    Reason string `json:"reason"`
}

type ContextMetadata struct {
    ActiveBranch    string   `json:"active_branch"`
    BudgetUsed      int      `json:"budget_used"`
    BudgetRemaining int      `json:"budget_remaining"`
    StaleWarnings   []string `json:"stale_warnings,omitempty"`
}

type FetchContextOutput struct {
    Context              string          `json:"context"`
    IncludedFiles        []IncludedFile  `json:"included_files"`
    Omitted              []OmittedFile   `json:"omitted,omitempty"`
    SuggestedNextQueries []string        `json:"suggested_next_queries,omitempty"`
    ContextMetadata      ContextMetadata `json:"context_metadata"`
}
```

### memory.propose_update

```go
type Operation struct {
    Operation       string `json:"operation" jsonschema:"create_file | append_section | replace_section | append_to_section | replace_section_content | archive_section | remove_section | rename_heading"`
    Path            string `json:"path"`
    SectionID       string `json:"section_id,omitempty"`
    Heading         string `json:"heading,omitempty"`
    HeadingLevel    int    `json:"heading_level,omitempty"`
    Occurrence      int    `json:"occurrence,omitempty"`
    ParentSectionID string `json:"parent_section_id,omitempty"`
    Content         string `json:"content,omitempty"`
    ArchivePath     string `json:"archive_path,omitempty"`
    Replacement     string `json:"replacement,omitempty"`
    NewHeading      string `json:"new_heading,omitempty"`
    NewHeadingLevel int    `json:"new_heading_level,omitempty"`
    IfExists        string `json:"if_exists,omitempty"`
    IfMissing       string `json:"if_missing,omitempty"`
}

type Source struct {
    Type string `json:"type" jsonschema:"file | test | user | session | inference | external"`
    Ref  string `json:"ref"`
}

type ProposeUpdateInput struct {
    Intent       string      `json:"intent" jsonschema:"update_current | update_shared | session_log | add_pitfall | record_decision | refresh_module | archive_stale | update_conventions"`
    Rationale    string      `json:"rationale"`
    Operations   []Operation `json:"operations"`
    ChangedFiles []string    `json:"changed_files,omitempty"`
    Sources      []Source    `json:"sources,omitempty"`
    Confidence   string      `json:"confidence,omitempty" jsonschema:"confirmed | inferred | user-provided"`
}

type SectionRef struct {
    File      string `json:"file"`
    SectionID string `json:"section_id"`
}

type Finding struct {
    Type                string `json:"type"`
    OperationIndex      int    `json:"operation_index,omitempty"`
    ApproximateLocation string `json:"approximate_location,omitempty"`
    AllowlistHint       string `json:"allowlist_hint,omitempty"`
}

type ProposeUpdateOutput struct {
    Status                string       `json:"status" jsonschema:"applied | staged | rejected"`
    AppliedAt             string       `json:"applied_at,omitempty"`
    StagingID             string       `json:"staging_id,omitempty"`
    StagingTTLSeconds     int          `json:"staging_ttl_seconds,omitempty"`
    HumanApprovalRequired bool         `json:"human_approval_required,omitempty"`
    ReviewCommand         string       `json:"review_command,omitempty"`
    ChangedFiles          []string     `json:"changed_files,omitempty"`
    AffectedSections      []SectionRef `json:"affected_sections,omitempty"`
    IndexUpdated          bool         `json:"index_updated,omitempty"`
    Warnings              []string     `json:"warnings,omitempty"`
    Reason                string       `json:"reason,omitempty"`
    Findings              []Finding    `json:"findings,omitempty"`
    RequiredAction        string       `json:"required_action,omitempty"`
    Message               string       `json:"message,omitempty"`
}
```

### memory.status

```go
type StatusInput struct{}

type StagedEntry struct {
    ID                  string   `json:"id"`
    Intent              string   `json:"intent"`
    AgeSeconds          int      `json:"age_seconds"`
    TTLRemainingSeconds int      `json:"ttl_remaining_seconds"`
    TargetFiles         []string `json:"target_files"`
    DriftDetected       bool     `json:"drift_detected"`
}

type SecurityStatus struct {
    LastSecretScan       string `json:"last_secret_scan"`
    AllowlistedRegions   int    `json:"allowlisted_regions"`
    UntrustedSources     int    `json:"untrusted_sources"`
}

type GitStatus struct {
    TrackLocal           bool `json:"track_local"`
    TrackSessions        bool `json:"track_sessions"`
    IgnoredLocalState    bool `json:"ignored_local_state"`
    MergeDriverInstalled bool `json:"merge_driver_installed"`
}

type LockStatus struct {
    Held                  bool `json:"held"`
    StaleRecoveriesLast24 int  `json:"stale_recoveries_last_24h"`
}

type StatusOutput struct {
    MemoryVersion     string         `json:"memory_version"`
    Repo              string         `json:"repo"`
    ActiveBranch      string         `json:"active_branch"`
    DurableFiles      int            `json:"durable_files"`
    ArchiveFiles      int            `json:"archive_files"`
    LocalSessions     int            `json:"local_sessions"`
    LocalCurrentFiles int            `json:"local_current_files"`
    OrphanLocalFiles  []string       `json:"orphan_local_files,omitempty"`
    IndexSizeBytes    int            `json:"index_size_bytes"`
    CurrentSizeBytes  int            `json:"current_size_bytes"`
    StagedUpdates     []StagedEntry  `json:"staged_updates,omitempty"`
    StaleNotes        []string       `json:"stale_notes,omitempty"`
    Security          SecurityStatus `json:"security"`
    Git               GitStatus      `json:"git"`
    Lock              LockStatus     `json:"lock"`
}
```

If the SDK can't express any of these via struct tags (e.g., nested types, enum-via-comment, omitempty handling), surface in [s2-results.md](../spikes/s2-results.md) findings and adjust.

## Alternatives considered

### Handwritten JSON-RPC stdio loop

Implementing MCP directly: read lines from stdin, decode JSON-RPC 2.0, dispatch to tool handlers, encode responses. **Rejected as primary** because the official SDK exists, is actively maintained, and tracks protocol versioning. Kept as **fallback** — our three-tool surface would be ~200 lines.

### Community SDKs

`metoro-io/mcp-golang` and similar third-party packages exist. **Rejected** per design doc v0.4 §0: official SDK is the explicit choice.

### Custom transport (TCP, HTTP)

Out of scope for v0.x. stdio is sufficient for local agent usage; remote/networked transports are a v1.x topic.

## Validation

[Spike S2](../spikes/s2-results.md) builds a minimal one-tool server (`ping`) and exercises it through Claude Code. Outcome captured there.

## References

- [Design Doc v0.4.1 §14, §15](../../agent-memory-design-doc-v0.4.1.md) — tool surface and contracts.
- [Spike S2 Results](../spikes/s2-results.md) — empirical validation.
- [Implementation Plan §3 S2](../../agent-memory-implementation-plan.md).
- [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk).
