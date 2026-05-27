package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agentfs "github.com/agent-memory/agent-memory/internal/fs"
	"github.com/agent-memory/agent-memory/internal/lock"
	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
)

// Status values for RebaseResult. StatusRejected is reused from the
// orchestrator's vocabulary (rebase rejection uses the same exit-code
// semantics as a propose_update rejection).
const (
	StatusRebased      = "rebased"
	StatusSkippedClean = "skipped_clean"
)

// Reason codes for RebaseResult. Stable wire identifiers — CLI / tests
// match against them.
const (
	ReasonForceRequired      = "force_required"
	ReasonUnresolvableDrift  = "unresolvable_drift"
	ReasonRebasePlanFailed   = "rebase_plan_failed"
	ReasonRebaseSecret       = "rebase_secret_detected"
	ReasonRebasePIIDetected  = "rebase_pii_detected"
	ReasonRebaseInvalidMD    = "rebase_invalid_markdown"
)

// RebaseResult is what RebaseStaged returns.
type RebaseResult struct {
	StagingID string        `json:"staging_id"`
	Status    string        `json:"status"`
	Reason    string        `json:"reason,omitempty"`
	Message   string        `json:"message,omitempty"`
	Forced    bool          `json:"forced,omitempty"`
	Drift     []DriftReport `json:"drift,omitempty"`
	Files     []string      `json:"files,omitempty"`    // staged files updated by rebase
	Findings  []Finding     `json:"findings,omitempty"` // populated on rebase_secret_detected
}

// RebaseStaged attempts to make a staged proposal applyable again after
// the target disk state changed since stage time.
//
// Phases:
//
//  1. Acquire the cross-process advisory lock.
//  2. Load proposal.json + target-checksums.json.
//  3. CheckDrift every target. Classify:
//     - file_present drift (file gone) → hard block
//     - file_absent drift (file appeared) → hard block
//     - section_resolvable drift (section gone) → hard block
//     - section_content_match where section is gone → hard block
//     - section_content_match where only hash differs → SOFT (rebaseable
//       with --force; we accept the new base bytes as the planning input)
//  4. No drift at all → return Status=skipped_clean.
//  5. Any hard block → return rejected with reason=unresolvable_drift.
//  6. All soft but --force not set → return rejected with
//     reason=force_required.
//  7. Re-plan: walk proposal.Request.Operations grouped by path; for
//     each file, read current disk bytes, ParseOperation +
//     Validate(schema) + Plan + Splice sequentially. The post-state per
//     file becomes the new staging/<id>/files/<path>.
//  8. Re-validate the new staged bytes (ValidateMarkdown +
//     allowlist+Scan). A re-splice that introduces a secret rejects the
//     rebase without touching disk.
//  9. WriteAtomic the new staged files. Update target-checksums.json
//     with refreshed hashes for content_match targets.
//
// Provenance is NOT re-checked — sources don't drift. Routing is NOT
// re-checked — the stored proposal already passed routing at stage time.
//
// Returns a Go error only for infrastructure failures (lock open, I/O).
// Application-level rejections come back in RebaseResult, NOT as errors,
// symmetric with ProposeUpdate / ApplyStaged.
func RebaseStaged(ctx context.Context, stagingID string, deps UpdateDeps, force bool) (*RebaseResult, error) {
	if deps.Manifest == nil || deps.Schema == nil || deps.MemoryDir == "" {
		return nil, errors.New("RebaseStaged: deps.Manifest, deps.Schema, deps.MemoryDir are required")
	}
	if !StagingExists(deps.MemoryDir, stagingID) {
		return &RebaseResult{
			StagingID: stagingID,
			Status:    StatusRejected,
			Reason:    ReasonStagingNotFound,
			Message:   fmt.Sprintf("no staging directory at %s", filepath.Join("staging", stagingID)),
		}, nil
	}

	// (1) lock
	waitTimeout := time.Duration(deps.Manifest.Concurrency.WaitTimeoutSeconds) * time.Second
	lk, err := lock.Acquire(
		filepath.Join(deps.MemoryDir, "meta", "lock"),
		lock.AcquireOpts{
			WaitTimeout: waitTimeout,
			Owner: lock.Metadata{
				OwnerKind: "cli-rebase",
				OpID:      stagingID,
			},
		},
	)
	if err != nil {
		if errors.Is(err, lock.ErrLockHeld) {
			return &RebaseResult{
				StagingID: stagingID,
				Status:    StatusRejected,
				Reason:    ReasonLockHeld,
				Message:   "memory lock is held by another writer",
			}, nil
		}
		return nil, fmt.Errorf("RebaseStaged: acquire lock: %w", err)
	}
	defer func() { _ = lk.Release() }()

	// (2) load
	proposal, err := LoadStaged(deps.MemoryDir, stagingID)
	if err != nil {
		return nil, fmt.Errorf("RebaseStaged: load proposal: %w", err)
	}
	targets, err := LoadStagedTargets(deps.MemoryDir, stagingID)
	if err != nil {
		return nil, fmt.Errorf("RebaseStaged: load targets: %w", err)
	}

	// (3) classify drifts
	var drifts []DriftReport
	var hardBlocks []DriftReport
	for _, t := range targets {
		report, cerr := CheckDrift(deps.MemoryDir, t)
		if cerr != nil {
			return nil, fmt.Errorf("RebaseStaged: %w", cerr)
		}
		if report == nil {
			continue
		}
		drifts = append(drifts, *report)
		if isHardBlock(deps.MemoryDir, t) {
			hardBlocks = append(hardBlocks, *report)
		}
	}

	// (4) no drift
	if len(drifts) == 0 {
		return &RebaseResult{
			StagingID: stagingID,
			Status:    StatusSkippedClean,
			Message:   "no drift detected; nothing to rebase",
		}, nil
	}

	// (5) hard blocks
	if len(hardBlocks) > 0 {
		return &RebaseResult{
			StagingID: stagingID,
			Status:    StatusRejected,
			Reason:    ReasonUnresolvableDrift,
			Message:   fmt.Sprintf("%d target(s) cannot be rebased: file/section is missing on disk", len(hardBlocks)),
			Drift:     drifts,
		}, nil
	}

	// (6) all soft — require --force
	if !force {
		return &RebaseResult{
			StagingID: stagingID,
			Status:    StatusRejected,
			Reason:    ReasonForceRequired,
			Message:   "drift is recoverable; re-run with --force to accept the new base content as planning input",
			Drift:     drifts,
		}, nil
	}

	// (7-9) re-plan + re-validate + write
	return rebaseReplan(ctx, stagingID, deps, proposal, targets, drifts)
}

// isHardBlock reports whether a drift on target t means rebase cannot
// recover even with --force. Hard-block conditions:
//   - RequireFileAbsent / RequireFilePresent / RequireSectionResolvable
//     all signal a binary "expected vs got" condition that --force can't
//     paper over.
//   - RequireSectionContentMatch is hard only when the section is gone
//     entirely; if it still resolves and just has a different hash,
//     --force can accept the new base.
func isHardBlock(memDir string, t OperationTarget) bool {
	switch t.Policy {
	case RequireFileAbsent, RequireFilePresent, RequireSectionResolvable:
		return true
	case RequireSectionContentMatch:
		// Soft only if section still resolves on disk.
		abs := filepath.Join(memDir, filepath.FromSlash(t.Path))
		src, err := os.ReadFile(abs)
		if err != nil {
			return true
		}
		sections, err := agentmd.ParseSections(src)
		if err != nil {
			return true
		}
		_, ok := agentmd.FindByID(sections, t.SectionID)
		return !ok
	}
	return true
}

// rebaseReplan does the actual re-splice + write work. Caller has
// already validated drift is all soft and --force was passed.
func rebaseReplan(
	ctx context.Context,
	stagingID string,
	deps UpdateDeps,
	proposal *StagedProposal,
	targets []OperationTarget,
	drifts []DriftReport,
) (*RebaseResult, error) {
	// Group ops by file in input order.
	fileOps := map[string][]OperationInput{}
	var fileOrder []string
	for _, op := range proposal.Request.Operations {
		rel := filepath.ToSlash(op.Path)
		if _, seen := fileOps[rel]; !seen {
			fileOrder = append(fileOrder, rel)
		}
		fileOps[rel] = append(fileOps[rel], op)
	}

	postState := map[string][]byte{}
	stageFilesDir := filepath.Join(deps.MemoryDir, "staging", stagingID, "files")

	for _, rel := range fileOrder {
		abs := filepath.Join(deps.MemoryDir, filepath.FromSlash(rel))
		src, err := os.ReadFile(abs)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("rebaseReplan: read %s: %w", rel, err)
		}

		cur := append([]byte(nil), src...)
		for i, in := range fileOps[rel] {
			op, perr := ParseOperation(in)
			if perr != nil {
				return rebaseRejection(stagingID, ReasonRebasePlanFailed,
					fmt.Sprintf("%s op[%d]: parse: %v", rel, i, perr), drifts), nil
			}
			if verr := op.Validate(deps.Schema); verr != nil {
				return rebaseRejection(stagingID, ReasonRebasePlanFailed,
					fmt.Sprintf("%s op[%d] (%s): validate: %v", rel, i, op.Kind(), verr), drifts), nil
			}
			splice, perr := op.Plan(cur)
			if perr != nil {
				return rebaseRejection(stagingID, ReasonRebasePlanFailed,
					fmt.Sprintf("%s op[%d] (%s): plan: %v", rel, i, op.Kind(), perr), drifts), nil
			}
			out, serr := agentmd.Splice(cur, []agentmd.SpliceOp{splice})
			if serr != nil {
				return rebaseRejection(stagingID, ReasonRebasePlanFailed,
					fmt.Sprintf("%s op[%d] (%s): splice: %v", rel, i, op.Kind(), serr), drifts), nil
			}
			cur = out
		}

		// Re-validate Markdown.
		if err := agentmd.ValidateMarkdown(cur); err != nil {
			return rebaseRejection(stagingID, ReasonRebaseInvalidMD,
				fmt.Sprintf("%s: %v", rel, err), drifts), nil
		}

		// Re-scan for secrets + PII — a malicious or accidental edit of
		// the on-disk base could introduce one that ends up in the
		// re-spliced post-state. Same security guarantee as
		// propose_update's apply path.
		if deps.Manifest.Security.SecretScan {
			regions, allowErr := ExtractAllowlistRegions(cur)
			if allowErr != nil {
				return rebaseRejection(stagingID, ReasonAllowlistParseError,
					fmt.Sprintf("%s: %v", rel, allowErr), drifts), nil
			}
			limits := AllowlistLimits{
				MaxBytesPerFile:   deps.Manifest.Security.AllowlistLimits.MaxBytesPerFile,
				MaxRegionsPerFile: deps.Manifest.Security.AllowlistLimits.MaxRegionsPerFile,
				MaxBytesPerRegion: deps.Manifest.Security.AllowlistLimits.MaxBytesPerRegion,
			}
			if limitMsg := CheckAllowlistLimits(regions, limits); limitMsg != "" {
				return rebaseRejection(stagingID, ReasonAllowlistLimitExceeded,
					fmt.Sprintf("%s: %s", rel, limitMsg), drifts), nil
			}
			scanOpts := DefaultScanOpts()
			scanOpts.Allowlist = regions
			scanOpts.PIIScanSSNAndCC = deps.Manifest.Security.PIIScan
			scanOpts.PIIScanEmail = deps.Manifest.Security.PIIScanEmail
			findings := Scan(cur, scanOpts)
			if len(findings) > 0 {
				reason := ReasonRebaseSecret
				if ClassifyFindings(findings) == ReasonPIIDetected {
					reason = ReasonRebasePIIDetected
				}
				return &RebaseResult{
					StagingID: stagingID,
					Status:    StatusRejected,
					Reason:    reason,
					Message:   fmt.Sprintf("%s: %d finding(s) after rebase", rel, len(findings)),
					Drift:     drifts,
					Findings:  findings,
				}, nil
			}
		}

		postState[rel] = cur
	}

	// All files re-spliced cleanly. Write them.
	for _, rel := range fileOrder {
		dst := filepath.Join(stageFilesDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return nil, fmt.Errorf("rebaseReplan: mkdir %s: %w", filepath.Dir(dst), err)
		}
		if err := agentfs.WriteAtomic(dst, postState[rel], 0644); err != nil {
			return nil, fmt.Errorf("rebaseReplan: write %s: %w", rel, err)
		}
	}

	// Refresh target hashes from current disk state for content_match
	// targets — those were the "soft drift" entries we accepted.
	for i := range targets {
		t := &targets[i]
		if t.Policy != RequireSectionContentMatch || t.SectionID == "" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(deps.MemoryDir, filepath.FromSlash(t.Path)))
		if err != nil {
			continue
		}
		if h := sectionHash(src, t.SectionID); h != "" {
			t.Hash = h
		}
	}
	tcBytes, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("rebaseReplan: marshal target-checksums: %w", err)
	}
	if err := agentfs.WriteAtomic(
		filepath.Join(deps.MemoryDir, "staging", stagingID, "target-checksums.json"),
		tcBytes, 0644,
	); err != nil {
		return nil, fmt.Errorf("rebaseReplan: write target-checksums.json: %w", err)
	}

	return &RebaseResult{
		StagingID: stagingID,
		Status:    StatusRebased,
		Forced:    true,
		Files:     append([]string(nil), fileOrder...),
		Drift:     drifts,
		Message:   fmt.Sprintf("re-planned %d file(s) against current base", len(fileOrder)),
	}, nil
}

func rebaseRejection(stagingID, reason, msg string, drifts []DriftReport) *RebaseResult {
	return &RebaseResult{
		StagingID: stagingID,
		Status:    StatusRejected,
		Reason:    reason,
		Message:   msg,
		Drift:     drifts,
	}
}
