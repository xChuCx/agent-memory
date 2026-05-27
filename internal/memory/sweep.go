package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SweepResult is what SweepStale returns. Expired is every staging entry
// whose age exceeded TTL at sweep time; Removed is the subset that
// SweepStale actually deleted (matches Expired unless DryRun was true).
type SweepResult struct {
	DryRun  bool              `json:"dry_run,omitempty"`
	Expired []ExpiredProposal `json:"expired,omitempty"`
	Removed []string          `json:"removed,omitempty"`
}

// ExpiredProposal is the per-entry shape inside SweepResult.Expired.
// Keeps just enough metadata for humans to recognise what got removed
// without re-reading staging/<id>/proposal.json (which is already gone).
type ExpiredProposal struct {
	StagingID  string `json:"staging_id"`
	Intent     string `json:"intent,omitempty"`
	Rationale  string `json:"rationale,omitempty"`
	StagedAt   string `json:"staged_at,omitempty"`
	AgeSeconds int    `json:"age_seconds"`
}

// SweepStale walks .agent-memory/staging/ and removes every proposal
// whose (now - StagedAt) exceeds ttl. Each removal is also recorded in
// meta/rejection-log.jsonl with reason="ttl_expired".
//
//   - dryRun=true → no filesystem changes; only Expired is populated.
//   - ttl <= 0 → returns SweepResult{} immediately (sweep disabled).
//   - Missing / malformed StagedAt on a staged proposal → conservative:
//     the entry is skipped (kept on disk) so corrupted-but-recent
//     proposals don't get nuked.
//   - Per-entry failures during removal are collected silently; the
//     remaining proposals still get processed. The user can re-run
//     sweep after fixing the underlying issue.
//
// SweepStale does NOT acquire the cross-process advisory lock — the
// caller (CLI subcommand) should. A propose_update writing into the
// same staging directory mid-sweep would be the only race, and the
// lock is the right primitive for that.
func SweepStale(memDir string, ttl time.Duration, dryRun bool) (*SweepResult, error) {
	res := &SweepResult{DryRun: dryRun}
	if ttl <= 0 {
		return res, nil
	}
	proposals, err := ListStaged(memDir)
	if err != nil {
		return nil, fmt.Errorf("SweepStale: list: %w", err)
	}
	now := time.Now().UTC()
	for _, p := range proposals {
		stagedAt, err := time.Parse(time.RFC3339, p.StagedAt)
		if err != nil {
			continue
		}
		age := now.Sub(stagedAt)
		if age <= ttl {
			continue
		}
		expired := ExpiredProposal{
			StagingID:  p.StagingID,
			Intent:     string(p.Request.Intent),
			Rationale:  p.Request.Rationale,
			StagedAt:   p.StagedAt,
			AgeSeconds: int(age.Seconds()),
		}
		res.Expired = append(res.Expired, expired)

		if dryRun {
			continue
		}
		if _, err := rejectStagedWithReason(memDir, p.StagingID, RejectionReasonTTLExpired); err != nil {
			// Don't fail the whole sweep — log via Expired only.
			continue
		}
		res.Removed = append(res.Removed, p.StagingID)
	}
	return res, nil
}

// rejectStagedWithReason removes staging/<stagingID>/ from disk AND
// appends a RejectionEntry to meta/rejection-log.jsonl with the supplied
// reason. Used by both interactive reject ("user_rejected") and the
// TTL sweeper ("ttl_expired"). Returns the entry that was logged so
// callers can surface it.
//
// Best-effort log write: a log write failure does NOT prevent the
// staging dir removal. The user has more pressing concerns than the
// audit log if the meta/ directory is unwriteable.
func rejectStagedWithReason(memDir, stagingID, reason string) (*RejectionEntry, error) {
	now := time.Now().UTC()
	entry := RejectionEntry{
		RejectedAt: now.Format(time.RFC3339),
		Reason:     reason,
		StagingID:  stagingID,
	}

	// Best-effort: pull the staged proposal's metadata BEFORE we delete
	// the directory. A failure here just leaves the metadata fields
	// empty — the entry still records the rejection itself.
	if p, err := LoadStaged(memDir, stagingID); err == nil && p != nil {
		entry.Intent = string(p.Request.Intent)
		entry.Rationale = p.Request.Rationale
		entry.Files = append([]string(nil), p.Files...)
		entry.StagedAt = p.StagedAt
		if stagedAt, err := time.Parse(time.RFC3339, p.StagedAt); err == nil {
			entry.AgeSeconds = int(now.Sub(stagedAt).Seconds())
		}
	}

	if err := os.RemoveAll(filepath.Join(memDir, "staging", stagingID)); err != nil {
		return nil, fmt.Errorf("rejectStagedWithReason: remove %s: %w", stagingID, err)
	}
	_ = AppendRejection(memDir, entry)
	return &entry, nil
}
