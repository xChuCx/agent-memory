package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	agentfs "github.com/agent-memory/agent-memory/internal/fs"
	"github.com/agent-memory/agent-memory/internal/lock"
	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
)

// StagedProposal is the envelope written to staging/<id>/proposal.json by
// stageProposal and read back by review/apply. Public so CLI renderers and
// test fixtures can inspect fields without re-parsing JSON themselves.
type StagedProposal struct {
	StagingID string         `json:"staging_id"`
	StagedAt  string         `json:"staged_at"`
	Request   ProposeRequest `json:"request"`
	Routing   Routing        `json:"routing"`
	Files     []string       `json:"files"`
}

// ApplyResult is what ApplyStaged returns. Status is one of
// StatusApplied / StatusRejected. On rejection, Reason is a stable code:
//   - ReasonStagingNotFound — no staging/<id>/ directory.
//   - ReasonTargetDrift     — the disk state changed since stage.
//   - ReasonLockHeld        — another writer holds the advisory lock.
type ApplyResult struct {
	StagingID string        `json:"staging_id"`
	Status    string        `json:"status"`
	Reason    string        `json:"reason,omitempty"`
	Message   string        `json:"message,omitempty"`
	Files     []string      `json:"files,omitempty"`
	Drift     []DriftReport `json:"drift,omitempty"`
}

// DriftReport describes a single target whose current disk state no longer
// matches the snapshot recorded at stage time. Surfaced verbatim to humans
// (review/apply CLI output) and to agents (response JSON) so they can
// decide between re-staging and abandoning.
type DriftReport struct {
	Path      string `json:"path"`
	SectionID string `json:"section_id,omitempty"`
	Policy    string `json:"policy"`
	Expected  string `json:"expected"`
	Found     string `json:"found"`
}

// ListStaged returns every staged proposal under memDir/staging/, sorted
// ascending by staging-id (which gives chronological order because the IDs
// start with a UTC timestamp).
//
// A missing staging/ directory returns (nil, nil) — that's the normal
// state right after `agent-memory init`.
//
// Malformed staging entries (missing proposal.json, unreadable JSON) are
// silently skipped. The reasoning: review should still surface the rest
// of the queue; a separate `doctor` command will eventually inspect
// quarantined staging entries.
func ListStaged(memDir string) ([]StagedProposal, error) {
	stageDir := filepath.Join(memDir, "staging")
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("ListStaged: read %s: %w", stageDir, err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	var out []StagedProposal
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := LoadStaged(memDir, e.Name())
		if err != nil {
			continue
		}
		// The directory name is the authoritative staging id; the
		// embedded JSON value is informational. Pin to the dir so
		// post-hoc renames (debugging, migration) don't confuse
		// downstream consumers.
		p.StagingID = e.Name()
		out = append(out, *p)
	}
	return out, nil
}

// LoadStaged reads staging/<stagingID>/proposal.json and returns the parsed
// envelope. Returns a wrapped error when the directory or file is missing.
func LoadStaged(memDir, stagingID string) (*StagedProposal, error) {
	path := filepath.Join(memDir, "staging", stagingID, "proposal.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadStaged %s: %w", stagingID, err)
	}
	var p StagedProposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("LoadStaged %s: parse proposal.json: %w", stagingID, err)
	}
	return &p, nil
}

// LoadStagedTargets reads staging/<stagingID>/target-checksums.json. Used
// by apply to re-verify drift policies against the current disk state.
func LoadStagedTargets(memDir, stagingID string) ([]OperationTarget, error) {
	path := filepath.Join(memDir, "staging", stagingID, "target-checksums.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadStagedTargets %s: %w", stagingID, err)
	}
	var targets []OperationTarget
	if err := json.Unmarshal(b, &targets); err != nil {
		return nil, fmt.Errorf("LoadStagedTargets %s: parse: %w", stagingID, err)
	}
	return targets, nil
}

// StagingExists reports whether staging/<stagingID>/ is a directory under
// memDir. Cheap probe used by review/apply/reject before doing heavier work.
func StagingExists(memDir, stagingID string) bool {
	st, err := os.Stat(filepath.Join(memDir, "staging", stagingID))
	return err == nil && st.IsDir()
}

// CheckDrift re-validates one OperationTarget against the current disk state
// of memDir. Returns:
//
//   - (nil, nil)        → no drift; safe to apply.
//   - (*DriftReport, nil) → drift detected; report describes the diff.
//   - (nil, err)        → I/O failure unrelated to drift; caller decides.
//
// Drift semantics by policy:
//
//   RequireSectionContentMatch: section must resolve by ID and its current
//     ContentHash must match the stored Hash. A missing file / missing
//     section / changed hash all count as drift.
//
//   RequireSectionResolvable: section must resolve by ID. Hash is ignored.
//     A missing file / missing section count as drift.
//
//   RequireFileAbsent: the file must not exist. A present file is drift.
//
//   RequireFilePresent: the file must exist. An absent file is drift.
func CheckDrift(memDir string, t OperationTarget) (*DriftReport, error) {
	abs := filepath.Join(memDir, filepath.FromSlash(t.Path))

	switch t.Policy {
	case RequireFileAbsent:
		_, err := os.Stat(abs)
		if err == nil {
			return &DriftReport{
				Path:     t.Path,
				Policy:   t.Policy.String(),
				Expected: "absent",
				Found:    "present",
			}, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("CheckDrift: stat %s: %w", abs, err)

	case RequireFilePresent:
		_, err := os.Stat(abs)
		if err == nil {
			return nil, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return &DriftReport{
				Path:     t.Path,
				Policy:   t.Policy.String(),
				Expected: "present",
				Found:    "absent",
			}, nil
		}
		return nil, fmt.Errorf("CheckDrift: stat %s: %w", abs, err)

	case RequireSectionResolvable, RequireSectionContentMatch:
		src, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return &DriftReport{
					Path:      t.Path,
					SectionID: t.SectionID,
					Policy:    t.Policy.String(),
					Expected:  "file present",
					Found:     "file missing",
				}, nil
			}
			return nil, fmt.Errorf("CheckDrift: read %s: %w", abs, err)
		}
		sections, err := agentmd.ParseSections(src)
		if err != nil {
			return &DriftReport{
				Path:      t.Path,
				SectionID: t.SectionID,
				Policy:    t.Policy.String(),
				Expected:  "file parseable",
				Found:     fmt.Sprintf("parse error: %v", err),
			}, nil
		}
		sec, ok := agentmd.FindByID(sections, t.SectionID)
		if !ok || sec == nil {
			return &DriftReport{
				Path:      t.Path,
				SectionID: t.SectionID,
				Policy:    t.Policy.String(),
				Expected:  fmt.Sprintf("section %q resolvable", t.SectionID),
				Found:     "section not found",
			}, nil
		}
		if t.Policy == RequireSectionContentMatch && t.Hash != "" && sec.ContentHash != t.Hash {
			return &DriftReport{
				Path:      t.Path,
				SectionID: t.SectionID,
				Policy:    t.Policy.String(),
				Expected:  t.Hash,
				Found:     sec.ContentHash,
			}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("CheckDrift: unknown policy %q", t.Policy)
}

// ApplyStaged applies a staged proposal:
//
//  1. Acquires the cross-process advisory lock (same lock ProposeUpdate uses).
//  2. Loads proposal.json + target-checksums.json.
//  3. CheckDrift on every target; any drift → ApplyResult{Status: rejected,
//     Reason: target_drift, Drift: [...]} (no I/O).
//  4. Reads each staged file under staging/<id>/files/ and WriteAtomics it
//     to its destination under memDir.
//  5. Re-indexes touched files (best-effort; failures don't roll back).
//  6. Removes staging/<id>/ from disk.
//
// Errors only for infrastructure failures (lock open, JSON parse fatal,
// destination write). Application-level rejections (drift, missing staging
// dir, lock held) come back in ApplyResult, NOT as Go errors. This mirrors
// ProposeUpdate's contract so the MCP wrapper can stay simple.
func ApplyStaged(ctx context.Context, stagingID string, deps UpdateDeps) (*ApplyResult, error) {
	if deps.Manifest == nil || deps.Schema == nil || deps.MemoryDir == "" {
		return nil, errors.New("ApplyStaged: deps.Manifest, deps.Schema, deps.MemoryDir are required")
	}
	if !StagingExists(deps.MemoryDir, stagingID) {
		return &ApplyResult{
			StagingID: stagingID,
			Status:    StatusRejected,
			Reason:    ReasonStagingNotFound,
			Message:   fmt.Sprintf("no staging directory at %s", filepath.Join("staging", stagingID)),
		}, nil
	}

	// Acquire the lock.
	waitTimeout := time.Duration(deps.Manifest.Concurrency.WaitTimeoutSeconds) * time.Second
	lk, err := lock.Acquire(
		filepath.Join(deps.MemoryDir, "meta", "lock"),
		lock.AcquireOpts{
			WaitTimeout: waitTimeout,
			Owner: lock.Metadata{
				OwnerKind: "cli-apply",
				OpID:      stagingID,
			},
		},
	)
	if err != nil {
		if errors.Is(err, lock.ErrLockHeld) {
			return &ApplyResult{
				StagingID: stagingID,
				Status:    StatusRejected,
				Reason:    ReasonLockHeld,
				Message:   "memory lock is held by another writer",
			}, nil
		}
		return nil, fmt.Errorf("ApplyStaged: acquire lock: %w", err)
	}
	defer func() { _ = lk.Release() }()

	proposal, err := LoadStaged(deps.MemoryDir, stagingID)
	if err != nil {
		return nil, fmt.Errorf("ApplyStaged: load proposal: %w", err)
	}
	targets, err := LoadStagedTargets(deps.MemoryDir, stagingID)
	if err != nil {
		return nil, fmt.Errorf("ApplyStaged: load targets: %w", err)
	}

	var drifts []DriftReport
	for _, t := range targets {
		report, derr := CheckDrift(deps.MemoryDir, t)
		if derr != nil {
			return nil, fmt.Errorf("ApplyStaged: %w", derr)
		}
		if report != nil {
			drifts = append(drifts, *report)
		}
	}
	if len(drifts) > 0 {
		return &ApplyResult{
			StagingID: stagingID,
			Status:    StatusRejected,
			Reason:    ReasonTargetDrift,
			Message:   fmt.Sprintf("%d target(s) drifted since stage", len(drifts)),
			Drift:     drifts,
		}, nil
	}

	stageFilesDir := filepath.Join(deps.MemoryDir, "staging", stagingID, "files")
	for _, rel := range proposal.Files {
		srcAbs := filepath.Join(stageFilesDir, filepath.FromSlash(rel))
		dstAbs := filepath.Join(deps.MemoryDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(srcAbs)
		if err != nil {
			return nil, fmt.Errorf("ApplyStaged: read staged %s: %w", rel, err)
		}
		if err := os.MkdirAll(filepath.Dir(dstAbs), 0755); err != nil {
			return nil, fmt.Errorf("ApplyStaged: mkdir %s: %w", filepath.Dir(dstAbs), err)
		}
		if err := agentfs.WriteAtomic(dstAbs, body, 0644); err != nil {
			return nil, fmt.Errorf("ApplyStaged: write %s: %w", rel, err)
		}
	}

	// Best-effort re-index. Index errors don't roll back the writes — bytes
	// are durable and rebuild-index can repair.
	if deps.Idx != nil {
		for _, rel := range proposal.Files {
			cat, _ := deps.Schema.CategoryForPath(rel)
			_ = reindexFile(ctx, deps.Idx, deps.MemoryDir, rel, cat)
		}
	}

	// Remove the staging directory now that everything is on disk. Failure
	// here is non-fatal: the apply succeeded; the user can clean up manually.
	_ = os.RemoveAll(filepath.Join(deps.MemoryDir, "staging", stagingID))

	return &ApplyResult{
		StagingID: stagingID,
		Status:    StatusApplied,
		Files:     append([]string(nil), proposal.Files...),
	}, nil
}

// RejectStaged removes staging/<stagingID>/ from disk. Cheap; no drift
// checks, no lock acquisition (the proposal isn't touching any other
// memory file).
//
// Returns ReasonStagingNotFound through the ApplyResult (NOT as a Go
// error) when the directory doesn't exist — symmetric with ApplyStaged's
// contract. Real filesystem errors during removal propagate as Go errors.
func RejectStaged(memDir, stagingID string) (*ApplyResult, error) {
	if !StagingExists(memDir, stagingID) {
		return &ApplyResult{
			StagingID: stagingID,
			Status:    StatusRejected,
			Reason:    ReasonStagingNotFound,
			Message:   fmt.Sprintf("no staging directory at %s", filepath.Join("staging", stagingID)),
		}, nil
	}
	if err := os.RemoveAll(filepath.Join(memDir, "staging", stagingID)); err != nil {
		return nil, fmt.Errorf("RejectStaged: remove %s: %w", stagingID, err)
	}
	return &ApplyResult{
		StagingID: stagingID,
		Status:    "rejected_by_user", // distinct from rejection-at-stage-time
	}, nil
}
