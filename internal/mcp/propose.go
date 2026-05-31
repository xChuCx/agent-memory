package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/index"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// ProposeUpdateInput is the JSON shape callers send to memory.propose_update.
// Fields mirror memory.ProposeRequest 1:1 — the wrapper exists so the SDK
// emits jsonschema descriptions that show up in the tool catalog.
type ProposeUpdateInput struct {
	Intent     string                  `json:"intent" jsonschema:"intent: update_current | update_shared | session_log | add_pitfall | record_decision | refresh_module | update_conventions | archive_stale"`
	Rationale  string                  `json:"rationale,omitempty" jsonschema:"short human-readable reason; shown in CLI status and used in the staging-id slug"`
	Operations []memory.OperationInput `json:"operations" jsonschema:"one or more structured edits to apply"`
	Sources    []memory.Source         `json:"sources,omitempty" jsonschema:"provenance citations (required for some categories, e.g. decisions)"`
	Confidence string                  `json:"confidence,omitempty" jsonschema:"confirmed | inferred | user-provided | stale | unknown"`
	Owner      memory.OwnerInfo        `json:"owner,omitempty" jsonschema:"identifier of the proposing agent; recorded in lock metadata"`
}

// ProposeUpdateOutput is the JSON shape memory.propose_update returns.
// Mirrors memory.ProposeResponse; same wrapping rationale as the input.
type ProposeUpdateOutput struct {
	Status               string                    `json:"status" jsonschema:"applied | staged | rejected"`
	Reason               string                    `json:"reason,omitempty" jsonschema:"on rejection: stable reason code (invalid_intent, secret_detected, ...)"`
	Message              string                    `json:"message,omitempty" jsonschema:"human-readable detail to accompany the reason code"`
	Routing              memory.Routing            `json:"routing,omitempty" jsonschema:"resolved approval routing for traceability"`
	StagingID            string                    `json:"staging_id,omitempty" jsonschema:"on staged: directory name under .agent-memory/staging/"`
	Files                []string                  `json:"files,omitempty" jsonschema:"forward-slash relative paths the proposal touched"`
	Findings             []memory.Finding          `json:"findings,omitempty" jsonschema:"on secret_detected: per-finding type + line"`
	Violations           []schema.SectionViolation `json:"violations,omitempty" jsonschema:"on validation_failed: per-section schema violations"`
	ProvenanceViolations []string                  `json:"provenance_violations,omitempty" jsonschema:"on provenance_violation: list of violation strings"`

	// Applied output (design §15.2).
	AppliedAt        string                   `json:"applied_at,omitempty" jsonschema:"on applied: RFC3339 UTC write time"`
	AffectedSections []memory.AffectedSection `json:"affected_sections,omitempty" jsonschema:"on applied: (file, section_id) pairs touched"`
	IndexUpdated     bool                     `json:"index_updated,omitempty" jsonschema:"on applied: whether the FTS index was refreshed"`
	Warnings         []string                 `json:"warnings,omitempty" jsonschema:"on applied: non-fatal advisories"`

	// Staged output (design §15.2).
	StagingTTLSeconds     int    `json:"staging_ttl_seconds,omitempty" jsonschema:"on staged: seconds until the proposal expires"`
	HumanApprovalRequired bool   `json:"human_approval_required,omitempty" jsonschema:"on staged: always true — a human must review"`
	ReviewCommand         string `json:"review_command,omitempty" jsonschema:"on staged: CLI command to inspect the proposal"`
}

// registerProposeUpdate wires memory.propose_update onto server. The closure
// captures root; every call re-loads manifest+schema+index so live edits
// between calls are seen.
//
// IMPORTANT: a rejected proposal (Status: "rejected") is a SUCCESSFUL tool
// call from the JSON-RPC perspective — the response body carries the reason.
// runProposeUpdate only returns a Go error for infrastructure failures
// (missing .agent-memory/, index open failure, etc.) so the agent sees
// rejection details in the structured output, not a transport-level error.
func registerProposeUpdate(server *mcpsdk.Server, root string, logger *slog.Logger) error {
	handler := func(ctx context.Context, req *mcpsdk.CallToolRequest, input ProposeUpdateInput) (
		*mcpsdk.CallToolResult,
		ProposeUpdateOutput,
		error,
	) {
		out, err := runProposeUpdate(ctx, root, logger, input)
		if err != nil {
			return nil, ProposeUpdateOutput{}, err
		}
		return nil, *out, nil
	}

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "memory.propose_update",
		Description: "Propose one or more structured edits to the project's " +
			".agent-memory/ files. Each operation is validated against the " +
			"schema, scanned for secrets, and checked for required provenance. " +
			"Depending on the intent and category, the proposal is either " +
			"applied immediately or staged under .agent-memory/staging/<id>/ " +
			"for human review via the apply/reject CLI commands. A rejected " +
			"proposal is reported in the response body, not as a transport error.",
	}, handler)
	return nil
}

// runProposeUpdate is the request-level handler. Separated from the closure
// so unit tests can drive it without spinning up an MCP transport. logger
// may be nil (tests) — UpdateDeps.log() falls back to a no-op logger.
func runProposeUpdate(ctx context.Context, root string, logger *slog.Logger, input ProposeUpdateInput) (*ProposeUpdateOutput, error) {
	memDir := filepath.Join(root, memoryDirName)

	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("memory.propose_update: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("memory.propose_update: load schema: %w", err)
	}

	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("memory.propose_update: open index: %w", err)
	}
	defer idx.Close()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("memory.propose_update: init index: %w", err)
	}

	resp, err := memory.ProposeUpdate(ctx, memory.ProposeRequest{
		Intent:     memory.Intent(input.Intent),
		Rationale:  input.Rationale,
		Operations: input.Operations,
		Sources:    input.Sources,
		Confidence: input.Confidence,
		Owner:      input.Owner,
	}, memory.UpdateDeps{
		Manifest:  manifest,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
		Logger:    logger,
	})
	if err != nil {
		return nil, fmt.Errorf("memory.propose_update: %w", err)
	}

	return &ProposeUpdateOutput{
		Status:               resp.Status,
		Reason:               resp.Reason,
		Message:              resp.Message,
		Routing:              resp.Routing,
		StagingID:            resp.StagingID,
		Files:                resp.Files,
		Findings:             resp.Findings,
		Violations:           resp.Violations,
		ProvenanceViolations: resp.ProvenanceViolations,

		AppliedAt:        resp.AppliedAt,
		AffectedSections: resp.AffectedSections,
		IndexUpdated:     resp.IndexUpdated,
		Warnings:         resp.Warnings,

		StagingTTLSeconds:     resp.StagingTTLSeconds,
		HumanApprovalRequired: resp.HumanApprovalRequired,
		ReviewCommand:         resp.ReviewCommand,
	}, nil
}
