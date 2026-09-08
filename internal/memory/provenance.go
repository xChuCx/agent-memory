package memory

import (
	"fmt"
	"strings"

	"github.com/xChuCx/agent-memory/internal/schema"
)

// GroundingReceipt carries the proof that a proposed mutation is grounded
// in an actively fetched context pack (SAR-008 §2).
// Resolves the Collision Probe identified by @marketdata-moth (#23170):
// Distinguishes Proof-of-Ingestion (freshness nonce: proves fetch was called)
// from Proof-of-Grounding (substantive locator: proves citation of content).
type GroundingReceipt struct {
	PackDigest string `json:"pack_digest,omitempty"` // SHA-256 of context pack (sha256:<hex>)
	ReadNonce  string `json:"read_nonce,omitempty"`  // Freshness token emitted by fetch (poi-<hash>-<ts>)
	Locator    string `json:"locator,omitempty"`     // Section ID, anchor quote, or line ref within the pack
}

// ValidateGrounding verifies whether a grounding receipt is structurally sound.
// If grounding is provided, it validates the format of PackDigest and ReadNonce.
// When requireLocator is true, it ensures a specific section or content locator
// is cited, preventing ungrounded decorative nonces.
func ValidateGrounding(g *GroundingReceipt, requireLocator bool) []string {
	if g == nil {
		return nil
	}
	var viols []string
	if g.PackDigest != "" && !strings.HasPrefix(g.PackDigest, "sha256:") {
		viols = append(viols, fmt.Sprintf("grounding: pack_digest %q must have 'sha256:' prefix", g.PackDigest))
	}
	if g.ReadNonce != "" && !strings.HasPrefix(g.ReadNonce, "poi-") {
		viols = append(viols, fmt.Sprintf("grounding: read_nonce %q must have 'poi-' prefix", g.ReadNonce))
	}
	if requireLocator && g.Locator == "" {
		viols = append(viols, "grounding: locator is required to bind proposal to specific pack content")
	}
	return viols
}

// Source describes one provenance entry attached to a propose_update
// proposal — where the agent claims this knowledge comes from. The
// fields mirror design doc v0.4.1 §23.5.
type Source struct {
	Type string `json:"type"`           // file | test | user | session | inference | external
	Ref  string `json:"ref,omitempty"`  // file path, test name, etc.
}

// ConfidenceLevels are the allowed values for the Confidence field on a
// propose_update input (design doc §23.5). Empty Confidence is also
// accepted — the orchestrator treats it as "unknown".
var ConfidenceLevels = []string{
	"confirmed",
	"inferred",
	"user-provided",
	"stale",
	"unknown",
}

// IsValidConfidence reports whether c is one of ConfidenceLevels or empty.
func IsValidConfidence(c string) bool {
	if c == "" {
		return true
	}
	for _, v := range ConfidenceLevels {
		if c == v {
			return true
		}
	}
	return false
}

// SourceTypes are the canonical source-type strings. Used as a hint; the
// per-category policy in schema.Provenance can narrow this further.
var SourceTypes = []string{
	"file", "test", "user", "session", "inference", "external",
}

// ProvenanceContext bundles the runtime facts the validator needs about
// a specific propose_update operation.
type ProvenanceContext struct {
	// Sources from the proposal.
	Sources []Source

	// Confidence from the proposal. May be empty.
	Confidence string

	// IsNewSection is true when the operation creates a new section (e.g.,
	// append_section, replace_section with if_missing=append) rather than
	// modifying an existing one. Used by Provenance.RequiredForNewSections.
	IsNewSection bool

	// Grounding from the proposal, linking the mutation to an actively
	// fetched context pack (SAR-008).
	Grounding *GroundingReceipt
}

// ValidateProvenance checks a ProvenanceContext against a category's
// Provenance policy from the schema. Returns a slice of human-readable
// violations; an empty slice means the proposal satisfies the policy.
//
// Checks (in order):
//   - Confidence is one of ConfidenceLevels (or empty).
//   - Every Source.Type is in schema's AllowedSourceTypes (when set).
//   - No Source.Type is in schema's ForbiddenSourceTypes.
//   - If Required → at least one Source must be present.
//   - If RequiredForNewSections AND ctx.IsNewSection → at least one Source.
//   - If Grounding is present → validate format (pack_digest and read_nonce).
func ValidateProvenance(policy schema.Provenance, ctx ProvenanceContext) []string {
	var violations []string

	if !IsValidConfidence(ctx.Confidence) {
		violations = append(violations,
			fmt.Sprintf("confidence %q is not one of %v", ctx.Confidence, ConfidenceLevels))
	}

	allowed := make(map[string]bool, len(policy.AllowedSourceTypes))
	for _, t := range policy.AllowedSourceTypes {
		allowed[t] = true
	}
	forbidden := make(map[string]bool, len(policy.ForbiddenSourceTypes))
	for _, t := range policy.ForbiddenSourceTypes {
		forbidden[t] = true
	}

	for i, s := range ctx.Sources {
		if s.Type == "" {
			violations = append(violations,
				fmt.Sprintf("source[%d]: type is required", i))
			continue
		}
		if forbidden[s.Type] {
			violations = append(violations,
				fmt.Sprintf("source[%d]: type %q is forbidden by schema", i, s.Type))
		}
		if len(allowed) > 0 && !allowed[s.Type] {
			violations = append(violations,
				fmt.Sprintf("source[%d]: type %q is not in allowed set %v", i, s.Type, policy.AllowedSourceTypes))
		}
	}

	requiresSources := policy.Required ||
		(policy.RequiredForNewSections && ctx.IsNewSection)
	if requiresSources && len(ctx.Sources) == 0 {
		if policy.Required {
			violations = append(violations,
				"sources are required by schema but none were provided")
		} else {
			violations = append(violations,
				"sources are required for new sections by schema but none were provided")
		}
	}

	if policy.GroundingRequired {
		if ctx.Grounding == nil {
			violations = append(violations, "grounding receipt is required by schema policy but was not provided")
		} else {
			violations = append(violations, ValidateGrounding(ctx.Grounding, true)...)
		}
	} else if ctx.Grounding != nil {
		violations = append(violations, ValidateGrounding(ctx.Grounding, false)...)
	}

	return violations
}
