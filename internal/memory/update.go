package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-memory/agent-memory/internal/config"
	agentfs "github.com/agent-memory/agent-memory/internal/fs"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/lock"
	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// ============================================================================
// Status / reason constants (stable wire identifiers)
// ============================================================================

// Status values returned in ProposeResponse.Status.
const (
	StatusApplied  = "applied"
	StatusStaged   = "staged"
	StatusRejected = "rejected"
)

// Reject reason codes. These are part of the propose_update wire contract:
// callers (CLI, MCP tool consumers, evals) match against them. Add a new
// code rather than reusing an existing one when introducing a new failure
// mode.
const (
	ReasonInvalidIntent         = "invalid_intent"
	ReasonInvalidOperation      = "invalid_operation"
	ReasonNoOperations          = "no_operations"
	ReasonValidationFailed      = "validation_failed"
	ReasonInvalidPath           = "invalid_path"
	ReasonUnknownCategory       = "unknown_category"
	ReasonServerManagedCategory = "server_managed_category"
	ReasonReadError             = "read_error"
	ReasonPlanFailed            = "plan_failed"
	ReasonSpliceFailed          = "splice_failed"
	ReasonInvalidMarkdown       = "invalid_markdown"
	ReasonAllowlistParseError   = "allowlist_parse_error"
	ReasonSecretDetected        = "secret_detected"
	ReasonProvenanceViolation   = "provenance_violation"
	ReasonServerOnlyCategory    = "server_only_category"
	ReasonLockHeld              = "lock_held"
	ReasonTargetDrift           = "target_drift"
	ReasonStagingNotFound       = "staging_not_found"
	ReasonAllowlistLimitExceeded = "allowlist_limit_exceeded"
	ReasonPIIDetected           = "pii_detected"
)

// ============================================================================
// Request / response types
// ============================================================================

// ProposeRequest is the orchestrator's input. Mirrors the propose_update MCP
// tool input verbatim — see design doc v0.4.1 §22.
type ProposeRequest struct {
	Intent     Intent           `json:"intent"`
	Rationale  string           `json:"rationale,omitempty"`
	Operations []OperationInput `json:"operations"`
	Sources    []Source         `json:"sources,omitempty"`
	Confidence string           `json:"confidence,omitempty"`
	Owner      OwnerInfo        `json:"owner,omitempty"`
}

// OwnerInfo identifies who is proposing the update. Used to populate the
// lock metadata (which is informational only — see internal/lock).
type OwnerInfo struct {
	ID   string `json:"id,omitempty"`
	Kind string `json:"kind,omitempty"` // "agent" | "cli" | ...
	OpID string `json:"op_id,omitempty"`
}

// ProposeResponse is what the orchestrator returns to the caller. The MCP
// tool serialises this verbatim.
type ProposeResponse struct {
	Status               string                    `json:"status"`
	Reason               string                    `json:"reason,omitempty"`
	Message              string                    `json:"message,omitempty"`
	Routing              Routing                   `json:"routing,omitempty"`
	StagingID            string                    `json:"staging_id,omitempty"`
	Files                []string                  `json:"files,omitempty"`
	Findings             []Finding                 `json:"findings,omitempty"`
	Violations           []schema.SectionViolation `json:"violations,omitempty"`
	ProvenanceViolations []string                  `json:"provenance_violations,omitempty"`

	// AutoStage reports git auto-stage / auto-commit outcomes when the
	// applied path produced a write AND manifest.git.auto_stage_changes
	// is true. nil on stage or reject; nil on apply when the feature is
	// off. See internal/memory/autostage.go.
	AutoStage *AutoStageResult `json:"auto_stage,omitempty"`
}

// UpdateDeps bundles the orchestrator's dependencies. Index is optional —
// nil means "skip re-index after apply" (used in tests that don't care
// about the FTS shadow).
type UpdateDeps struct {
	Manifest  *config.Manifest
	Schema    *schema.Schema
	MemoryDir string       // absolute path to .agent-memory/
	Idx       *index.Index // optional
}

// ============================================================================
// ProposeUpdate — the pipeline
// ============================================================================

// ProposeUpdate runs the full propose_update pipeline:
//
//  1. Validate intent + non-empty operations.
//  2. session_log intent: rewrite each op's path to sessions/<UTC-today>.md
//     unless it already lives under sessions/.
//  3. Parse + per-op Validate() against the schema.
//  4. Validate paths (ValidateMemoryPath) and resolve each op's Category;
//     reject unknown / server_managed categories.
//  5. Acquire the .agent-memory/meta/lock advisory lock with WaitTimeout
//     from manifest.Concurrency.
//  6. For each unique file in the proposal:
//     a. Read current bytes (empty if file is absent).
//     b. Apply this file's ops SEQUENTIALLY in memory (each op's Plan sees
//     the post-previous-op bytes).
//     c. ValidateMarkdown on the final bytes.
//     d. Per-section schema validation on the final bytes.
//     e. ExtractAllowlistRegions on the final bytes.
//     f. Scan(final, ScanOpts{Allowlist, ...}) — reject on any finding.
//  7. Provenance validation against the dominant category's policy.
//  8. Routing: combine per-op routings. server_only → reject; stage → stage;
//     apply → apply.
//  9. Apply (write atomic + re-index) OR Stage (write proposal artefacts).
//
// Any single step's failure short-circuits to ProposeResponse{Status: rejected}
// with a stable Reason code.
func ProposeUpdate(ctx context.Context, req ProposeRequest, deps UpdateDeps) (*ProposeResponse, error) {
	// (1) intent + non-empty ops
	if !IsValidIntent(req.Intent) {
		return reject(ReasonInvalidIntent, fmt.Sprintf("intent %q is not recognised", req.Intent)), nil
	}
	if len(req.Operations) == 0 {
		return reject(ReasonNoOperations, "at least one operation is required"), nil
	}
	if deps.Manifest == nil || deps.Schema == nil || deps.MemoryDir == "" {
		return nil, errors.New("ProposeUpdate: deps.Manifest, deps.Schema, deps.MemoryDir are required")
	}

	// (2) session_log path rewrite — done on the raw OperationInput before
	// we parse, so the parsed op carries the rewritten path verbatim.
	if req.Intent == IntentSessionLog {
		todayPath := sessionsPathForToday()
		for i := range req.Operations {
			p := req.Operations[i].Path
			if !strings.HasPrefix(p, "sessions/") {
				req.Operations[i].Path = todayPath
			}
		}
	}

	// (3) Parse + per-op validate
	ops := make([]Operation, 0, len(req.Operations))
	for i, in := range req.Operations {
		op, err := ParseOperation(in)
		if err != nil {
			return reject(ReasonInvalidOperation,
				fmt.Sprintf("op[%d]: %v", i, err)), nil
		}
		if err := op.Validate(deps.Schema); err != nil {
			return reject(ReasonValidationFailed,
				fmt.Sprintf("op[%d]: %v", i, err)), nil
		}
		ops = append(ops, op)
	}

	// (4) Path validation + category resolution
	resolved := make([]opCat, 0, len(ops))
	for i, op := range ops {
		rel := filepath.ToSlash(op.Path())
		if _, err := agentfs.ValidateMemoryPath(deps.MemoryDir, rel); err != nil {
			return reject(ReasonInvalidPath,
				fmt.Sprintf("op[%d]: %v", i, err)), nil
		}
		cat, ok := deps.Schema.CategoryForPath(rel)
		if !ok {
			return reject(ReasonUnknownCategory,
				fmt.Sprintf("op[%d]: no category matches %q", i, rel)), nil
		}
		if cat.ServerManaged {
			return reject(ReasonServerManagedCategory,
				fmt.Sprintf("op[%d]: category %q is server_managed (path %q)", i, cat.Name, rel)), nil
		}
		resolved = append(resolved, opCat{op: op, rel: rel, category: cat})
	}

	// (5) Acquire the cross-process lock. Best-effort owner metadata.
	waitTimeout := time.Duration(deps.Manifest.Concurrency.WaitTimeoutSeconds) * time.Second
	lk, err := lock.Acquire(
		filepath.Join(deps.MemoryDir, "meta", "lock"),
		lock.AcquireOpts{
			WaitTimeout: waitTimeout,
			Owner: lock.Metadata{
				OwnerID:   req.Owner.ID,
				OwnerKind: req.Owner.Kind,
				OpID:      req.Owner.OpID,
			},
		},
	)
	if err != nil {
		if errors.Is(err, lock.ErrLockHeld) {
			return reject(ReasonLockHeld, "memory lock is held by another writer"), nil
		}
		return nil, fmt.Errorf("ProposeUpdate: acquire lock: %w", err)
	}
	defer func() { _ = lk.Release() }()

	// (6) Per-file: read → splice → validate → secret scan.
	// preState maps rel → bytes read from disk (nil if absent).
	// postState maps rel → final bytes after applying all ops on this file.
	// fileOps maps rel → slice of opCat in input order.
	preState := map[string][]byte{}
	postState := map[string][]byte{}
	fileOps := map[string][]opCat{}
	fileOrder := []string{} // preserve first-appearance order
	for _, oc := range resolved {
		if _, seen := fileOps[oc.rel]; !seen {
			fileOrder = append(fileOrder, oc.rel)
		}
		fileOps[oc.rel] = append(fileOps[oc.rel], oc)
	}

	for _, rel := range fileOrder {
		src, err := readPreState(deps.MemoryDir, rel)
		if err != nil {
			return reject(ReasonReadError, fmt.Sprintf("%s: %v", rel, err)), nil
		}
		preState[rel] = src

		// Apply ops sequentially on this file's in-memory bytes.
		cur := append([]byte(nil), src...)
		for i, oc := range fileOps[rel] {
			splice, err := oc.op.Plan(cur)
			if err != nil {
				return reject(ReasonPlanFailed,
					fmt.Sprintf("%s op[%d] (%s): %v", rel, i, oc.op.Kind(), err)), nil
			}
			out, err := agentmd.Splice(cur, []agentmd.SpliceOp{splice})
			if err != nil {
				return reject(ReasonSpliceFailed,
					fmt.Sprintf("%s op[%d] (%s): %v", rel, i, oc.op.Kind(), err)), nil
			}
			cur = out
		}
		postState[rel] = cur

		// Markdown validation on the final bytes.
		if err := agentmd.ValidateMarkdown(cur); err != nil {
			return reject(ReasonInvalidMarkdown,
				fmt.Sprintf("%s: %v", rel, err)), nil
		}

		// Per-section schema validation: validate ONLY sections this
		// proposal created or modified. Legacy untouched sections from
		// before the schema landed in DefaultSchema stay valid until the
		// user edits them. Skipped when the category declares no
		// SectionSchema.
		//
		// "Affected" is determined by comparing each section's DIRECT
		// body (heading + immediate prose, excluding nested descendants)
		// pre vs post. This matters when an op like `append_section`
		// adds a child under an existing parent: the parent's full
		// content range expands (new child is now inside it), but the
		// parent's own body didn't change. directBody captures the
		// parent-vs-descendants distinction.
		cat := fileOps[rel][0].category
		if cat.SectionSchema != nil {
			postSections, perr := agentmd.ParseSections(cur)
			if perr != nil {
				return reject(ReasonInvalidMarkdown,
					fmt.Sprintf("%s: parse sections: %v", rel, perr)), nil
			}
			isWholeFileNew := len(preState[rel]) == 0
			preBodyByID := map[string][]byte{}
			if !isWholeFileNew {
				preSections, _ := agentmd.ParseSections(preState[rel])
				for i, s := range preSections {
					if s.AnchorID != "" {
						preBodyByID[s.AnchorID] = directBody(preState[rel], preSections, i)
					}
				}
			}
			var allViolations []schema.SectionViolation
			for i, sec := range postSections {
				affected := isWholeFileNew
				if !affected && sec.AnchorID != "" {
					preBody, wasPresent := preBodyByID[sec.AnchorID]
					postBody := directBody(cur, postSections, i)
					affected = !wasPresent || !bytes.Equal(preBody, postBody)
				}
				if !affected {
					continue
				}
				bodyStart := findSectionBodyStart(cur, sec.ByteStart)
				body := cur[bodyStart:sec.ByteEnd]
				v := schema.ValidateSection(cat, body)
				// Annotate with the section's identity so the response
				// message tells the agent WHICH section failed.
				ident := sec.AnchorID
				if ident == "" {
					ident = fmt.Sprintf("%q (no @id)", sec.HeadingText)
				} else {
					ident = "@id=" + ident
				}
				for vi := range v {
					v[vi].Message = fmt.Sprintf("section %s: %s", ident, v[vi].Message)
				}
				allViolations = append(allViolations, v...)
			}
			if len(allViolations) > 0 {
				return rejectWithViolations(ReasonValidationFailed,
					fmt.Sprintf("%s: %d section schema violation(s)", rel, len(allViolations)),
					allViolations), nil
			}
		}

		// Allowlist extract + limits check + secret/PII scan on the
		// final bytes.
		if deps.Manifest.Security.SecretScan {
			regions, allowErr := ExtractAllowlistRegions(cur)
			if allowErr != nil {
				return reject(ReasonAllowlistParseError,
					fmt.Sprintf("%s: %v", rel, allowErr)), nil
			}
			limits := AllowlistLimits{
				MaxBytesPerFile:   deps.Manifest.Security.AllowlistLimits.MaxBytesPerFile,
				MaxRegionsPerFile: deps.Manifest.Security.AllowlistLimits.MaxRegionsPerFile,
				MaxBytesPerRegion: deps.Manifest.Security.AllowlistLimits.MaxBytesPerRegion,
			}
			if limitMsg := CheckAllowlistLimits(regions, limits); limitMsg != "" {
				return reject(ReasonAllowlistLimitExceeded,
					fmt.Sprintf("%s: %s", rel, limitMsg)), nil
			}
			scanOpts := DefaultScanOpts()
			scanOpts.Allowlist = regions
			scanOpts.PIIScanSSNAndCC = deps.Manifest.Security.PIIScan
			scanOpts.PIIScanEmail = deps.Manifest.Security.PIIScanEmail
			findings := Scan(cur, scanOpts)
			if len(findings) > 0 {
				reason := ClassifyFindings(findings)
				return rejectWithFindings(reason,
					fmt.Sprintf("%s: %d finding(s)", rel, len(findings)),
					findings), nil
			}
		}
	}

	// (7) Provenance — checked against the dominant category. When ops touch
	// multiple categories, the strictest policy wins; for M3 we use the first
	// op's category as the policy source (almost all proposals are single-
	// category; the orchestrator can be extended later).
	dominant := resolved[0].category
	pctx := ProvenanceContext{
		Sources:      req.Sources,
		Confidence:   req.Confidence,
		IsNewSection: containsNewSectionOp(ops),
	}
	if provViols := ValidateProvenance(dominant.Provenance, pctx); len(provViols) > 0 {
		return rejectWithProvViolations(ReasonProvenanceViolation,
			fmt.Sprintf("category %q: %d provenance violation(s)", dominant.Name, len(provViols)),
			provViols), nil
	}

	// (8) Routing — combine per-op routings.
	routings := make([]Routing, 0, len(ops))
	for _, op := range ops {
		r, rerr := DecideRouting(req.Intent, op, deps.Manifest)
		if rerr != nil {
			return reject(ReasonInvalidIntent, rerr.Error()), nil
		}
		routings = append(routings, r)
	}
	final := CombineRoutings(routings)

	switch final.Mode {
	case schema.ApprovalServerOnly:
		return rejectWithRouting(ReasonServerOnlyCategory,
			"routing resolved to server_only; agent cannot write this category",
			final), nil
	case schema.ApprovalApply:
		return applyImmediately(ctx, deps, fileOrder, postState, fileOps, final, req.Intent, req.Rationale)
	case schema.ApprovalStage:
		return stageProposal(req, deps, fileOrder, preState, postState, resolved, final)
	}
	return nil, fmt.Errorf("ProposeUpdate: unknown approval mode %q", final.Mode)
}

// ============================================================================
// Apply — write post-state atomically + re-index
// ============================================================================

// applyImmediately writes post-state bytes to disk atomically for every
// affected file and re-indexes them. The lock is held by the caller.
//
// intent and rationale are passed through to the optional git auto-stage
// step at the end so the commit message can identify the change.
func applyImmediately(
	ctx context.Context,
	deps UpdateDeps,
	fileOrder []string,
	postState map[string][]byte,
	fileOps map[string][]opCat,
	routing Routing,
	intent Intent,
	rationale string,
) (*ProposeResponse, error) {
	for _, rel := range fileOrder {
		abs := filepath.Join(deps.MemoryDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return nil, fmt.Errorf("applyImmediately: mkdir %s: %w", filepath.Dir(abs), err)
		}
		if err := agentfs.WriteAtomic(abs, postState[rel], 0644); err != nil {
			return nil, fmt.Errorf("applyImmediately: write %s: %w", rel, err)
		}
	}

	// Re-index touched files. Errors here do NOT roll back the write — the
	// bytes are durable and the index can be rebuilt via `rebuild-index`.
	// We log nothing (no logger plumbed in); future T3.7 follow-up wires
	// slog through deps.
	if deps.Idx != nil {
		for _, rel := range fileOrder {
			cat := fileOps[rel][0].category
			_ = reindexFile(ctx, deps.Idx, deps.MemoryDir, rel, cat)
		}
	}

	// Best-effort git auto-stage + auto-commit per manifest.git.* flags.
	// Result attached to the response; no error path can fail the apply
	// here — the bytes already landed via WriteAtomic.
	repoRoot := filepath.Dir(deps.MemoryDir)
	autoStage := maybeAutoStage(deps, repoRoot, fileOrder, intent, rationale)

	return &ProposeResponse{
		Status:    StatusApplied,
		Files:     append([]string(nil), fileOrder...),
		Routing:   routing,
		AutoStage: &autoStage,
	}, nil
}

// reindexFile re-parses one applied file and upserts its sections + FileDoc.
// Best-effort: any error is silently swallowed at the caller. The next
// rebuild-index or status refresh will repair.
func reindexFile(ctx context.Context, idx *index.Index, memDir, rel string, cat schema.Category) error {
	abs := filepath.Join(memDir, filepath.FromSlash(rel))
	src, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	sections, err := agentmd.ParseSections(src)
	if err != nil {
		return err
	}
	docs := make([]index.SectionDoc, 0, len(sections))
	for _, sec := range sections {
		if sec.AnchorID == "" {
			continue // sections without IDs are not indexed
		}
		bodyStart := findSectionBodyStart(src, sec.ByteStart)
		docs = append(docs, index.SectionDoc{
			File:         rel,
			SectionID:    sec.AnchorID,
			Heading:      sec.HeadingText,
			HeadingLevel: sec.HeadingLevel,
			Title:        sec.HeadingText,
			Content:      string(src[bodyStart:sec.ByteEnd]),
			ByteStart:    sec.ByteStart,
			ByteEnd:      sec.ByteEnd,
			ContentHash:  sec.ContentHash,
		})
	}
	if err := idx.UpsertSections(ctx, docs); err != nil {
		return err
	}
	sum := sha256.Sum256(src)
	return idx.UpsertFile(ctx, index.FileDoc{
		File:         rel,
		Category:     cat.Name,
		LastModified: time.Now().UTC().Format(time.RFC3339),
		Committed:    cat.GitTracked,
		LocalState:   !cat.GitTracked,
		SizeBytes:    len(src),
		Checksum:     "sha256:" + hex.EncodeToString(sum[:]),
	})
}

// ============================================================================
// Stage — write proposal artefacts under .agent-memory/staging/<id>/
// ============================================================================

// stageProposal materialises the proposal under .agent-memory/staging/<id>/:
//
//	proposal.json         — full ProposeRequest + Routing + per-op metadata
//	target-checksums.json — array of OperationTarget with Hash filled from
//	                        pre-state when policy requires content match
//	files/<rel-path>      — post-state bytes for every affected file
//
// The M5 apply CLI (`agent-memory apply <id>`) re-reads these artefacts,
// re-verifies the drift policies against the now-current disk state, and
// performs the byte-level writes.
func stageProposal(
	req ProposeRequest,
	deps UpdateDeps,
	fileOrder []string,
	preState map[string][]byte,
	postState map[string][]byte,
	resolved []opCat,
	routing Routing,
) (*ProposeResponse, error) {
	stagingID := makeStagingID(req)
	stageDir := filepath.Join(deps.MemoryDir, "staging", stagingID)
	if err := os.MkdirAll(filepath.Join(stageDir, "files"), 0755); err != nil {
		return nil, fmt.Errorf("stageProposal: mkdir %s: %w", stageDir, err)
	}

	// Write post-state files under staging/<id>/files/<rel-path>.
	for _, rel := range fileOrder {
		dst := filepath.Join(stageDir, "files", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return nil, fmt.Errorf("stageProposal: mkdir %s: %w", filepath.Dir(dst), err)
		}
		if err := agentfs.WriteAtomic(dst, postState[rel], 0644); err != nil {
			return nil, fmt.Errorf("stageProposal: write %s: %w", dst, err)
		}
	}

	// target-checksums.json: materialise OperationTarget.Hash from pre-state
	// for content-match drift checks.
	var targets []OperationTarget
	for _, oc := range resolved {
		for _, t := range oc.op.Targets() {
			if t.Policy == RequireSectionContentMatch && t.SectionID != "" {
				src := preState[oc.rel]
				if h := sectionHash(src, t.SectionID); h != "" {
					t.Hash = h
				}
			}
			targets = append(targets, t)
		}
	}
	tcBytes, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("stageProposal: marshal target-checksums: %w", err)
	}
	if err := agentfs.WriteAtomic(
		filepath.Join(stageDir, "target-checksums.json"), tcBytes, 0644,
	); err != nil {
		return nil, fmt.Errorf("stageProposal: write target-checksums.json: %w", err)
	}

	// proposal.json: archived for audit + replay.
	envelope := StagedProposal{
		StagingID: stagingID,
		StagedAt:  time.Now().UTC().Format(time.RFC3339),
		Request:   req,
		Routing:   routing,
		Files:     fileOrder,
	}
	pbytes, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("stageProposal: marshal proposal.json: %w", err)
	}
	if err := agentfs.WriteAtomic(
		filepath.Join(stageDir, "proposal.json"), pbytes, 0644,
	); err != nil {
		return nil, fmt.Errorf("stageProposal: write proposal.json: %w", err)
	}

	return &ProposeResponse{
		Status:    StatusStaged,
		Files:     append([]string(nil), fileOrder...),
		Routing:   routing,
		StagingID: stagingID,
	}, nil
}

// sectionHash returns the ContentHash of the section identified by id in src,
// or "" if not found.
func sectionHash(src []byte, id string) string {
	sections, err := agentmd.ParseSections(src)
	if err != nil {
		return ""
	}
	sec, ok := agentmd.FindByID(sections, id)
	if !ok {
		return ""
	}
	return sec.ContentHash
}

// makeStagingID builds the staging directory name. Format:
//
//	<UTC YYYYMMDDTHHMMSS>-<slug(intent + rationale, max 40 chars)>
//
// The timestamp prefix keeps directory listings naturally chronologically
// ordered. The slug appendix gives humans a hint of WHAT was staged.
func makeStagingID(req ProposeRequest) string {
	ts := time.Now().UTC().Format("20060102T150405")
	hint := string(req.Intent)
	if req.Rationale != "" {
		hint = hint + "-" + req.Rationale
	}
	slug := slugify(hint, 40)
	if slug == "" {
		slug = "proposal"
	}
	return ts + "-" + slug
}

// slugify lower-cases s, drops everything outside [a-z0-9], collapses runs
// of '-' to single dashes, and trims to maxLen.
func slugify(s string, maxLen int) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) > maxLen {
		out = strings.TrimRight(out[:maxLen], "-")
	}
	return out
}

// ============================================================================
// Helpers
// ============================================================================

// readPreState returns the current bytes of rel under memDir, or (nil, nil)
// if the file does not exist. Real read errors propagate.
func readPreState(memDir, rel string) ([]byte, error) {
	abs := filepath.Join(memDir, filepath.FromSlash(rel))
	b, err := os.ReadFile(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// containsNewSectionOp returns true if any op in ops creates a new section
// (informs ProvenanceContext.IsNewSection).
func containsNewSectionOp(ops []Operation) bool {
	for _, op := range ops {
		switch op.Kind() {
		case "create_file", "append_section":
			return true
		}
	}
	return false
}

// sessionsPathForToday returns "sessions/YYYY-MM-DD.md" in the UTC timezone.
// Used by the session_log intent path rewrite.
func sessionsPathForToday() string {
	return path.Join("sessions", time.Now().UTC().Format("2006-01-02")+".md")
}

// opCat pairs an Operation with the resolved Category and forward-slash
// relative path. Defined here so it's accessible from applyImmediately
// without re-resolving.
type opCat struct {
	op       Operation
	rel      string
	category schema.Category
}

// ============================================================================
// Reject helpers — produce a consistent ProposeResponse for each failure mode
// ============================================================================

func reject(reason, msg string) *ProposeResponse {
	return &ProposeResponse{Status: StatusRejected, Reason: reason, Message: msg}
}

func rejectWithFindings(reason, msg string, findings []Finding) *ProposeResponse {
	return &ProposeResponse{
		Status:   StatusRejected,
		Reason:   reason,
		Message:  msg,
		Findings: findings,
	}
}

func rejectWithViolations(reason, msg string, v []schema.SectionViolation) *ProposeResponse {
	return &ProposeResponse{
		Status:     StatusRejected,
		Reason:     reason,
		Message:    msg,
		Violations: v,
	}
}

func rejectWithProvViolations(reason, msg string, v []string) *ProposeResponse {
	return &ProposeResponse{
		Status:               StatusRejected,
		Reason:               reason,
		Message:              msg,
		ProvenanceViolations: v,
	}
}

func rejectWithRouting(reason, msg string, r Routing) *ProposeResponse {
	return &ProposeResponse{
		Status:  StatusRejected,
		Reason:  reason,
		Message: msg,
		Routing: r,
	}
}
