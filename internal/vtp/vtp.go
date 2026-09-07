package vtp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// NormalizeLF applies canonical LF normalization (SAR-002) prior to hashing.
func NormalizeLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// ComputeDigest returns the canonical SHA-256 hex string with CRLF normalization.
func ComputeDigest(b []byte) string {
	normalized := NormalizeLF(b)
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

// VerifyReceipt verifies a worker's TaskReceipt against live execution output and diffs.
// It automatically derives account distinctness (HasDistinctAccountIDs) from authenticated IDs
// and records operator independence as UNKNOWN unless backed by separate topological attestations.
func VerifyReceipt(spec *TaskSpec, receipt *TaskReceipt, actualStdout, actualDiff []byte, actualExitCode int, verifier string) (*TaskVerify, error) {
	if spec == nil || receipt == nil {
		return nil, errors.New("spec and receipt must not be nil")
	}
	if spec.TaskID != receipt.TaskID {
		return nil, fmt.Errorf("task ID mismatch: spec=%s, receipt=%s", spec.TaskID, receipt.TaskID)
	}

	actualStdoutSHA := ComputeDigest(actualStdout)
	actualDiffSHA := ComputeDigest(actualDiff)

	// Build evidence payload
	evidencePayload := fmt.Sprintf("spec:%s|receipt:%s|stdout_sha:%s|diff_sha:%s|exit:%d",
		spec.TaskID, receipt.IdempotencyKey, actualStdoutSHA, actualDiffSHA, actualExitCode)
	evidenceSHA := ComputeDigest([]byte(evidencePayload))

	distinct := HasDistinctAccountIDs(receipt.Worker, verifier, spec.Creator)
	hermStatus := DeriveHermeticityStatus(spec, receipt)
	isHermetic := hermStatus == HermeticStatusVerifiedHermetic

	verify := &TaskVerify{
		Protocol:             ProtocolVersion,
		Type:                 "VERIFY",
		TaskID:               spec.TaskID,
		Verifier:             verifier,
		OracleType:           spec.Oracle.Type,
		EvidenceSHA256:       evidenceSHA,
		DistinctAccountIDs:   distinct,
		IsDisjointSeat:       distinct,
		OperatorIndependence: "UNKNOWN",
		HermeticityStatus:    hermStatus,
		IsHermetic:           isHermetic,
	}

	// Exit code check
	if actualExitCode != 0 {
		verify.Verdict = "FAIL"
		verify.Basis = "NON_ZERO_EXIT"
		return verify, nil
	}

	// Digest checks
	if receipt.Execution.StdoutSHA256 != "" && receipt.Execution.StdoutSHA256 != actualStdoutSHA {
		verify.Verdict = "FAIL"
		verify.Basis = "STDOUT_DIGEST_MISMATCH"
		return verify, nil
	}
	if receipt.Execution.DiffHunksSHA256 != "" && receipt.Execution.DiffHunksSHA256 != actualDiffSHA {
		verify.Verdict = "FAIL"
		verify.Basis = "DIFF_DIGEST_MISMATCH"
		return verify, nil
	}

	verify.Verdict = "PASS"
	verify.Basis = "FACT_CONSISTENT"
	return verify, nil
}

// DeriveHermeticityStatus computes the tri-state hermeticity status from verifiable evidence
// rather than blindly trusting worker self-declarations (SAR-006 / second-thought audit #22345).
func DeriveHermeticityStatus(spec *TaskSpec, receipt *TaskReceipt) HermeticityStatus {
	if receipt == nil || spec == nil {
		return HermeticStatusUnknown
	}
	// If worker explicitly declares non-hermetic, trust the admission of non-hermeticity
	if !receipt.Execution.Hermetic {
		return HermeticStatusDeclaredNonHermetic
	}
	// Worker claims hermeticity: verify whether sandbox isolation proof is provided
	if receipt.Execution.Sandbox != nil && receipt.Execution.Sandbox.PolicyDigest != "" {
		return HermeticStatusVerifiedHermetic
	}
	// Unattested self-declaration defaults to UNKNOWN (must not be trusted for Fast-Path)
	return HermeticStatusUnknown
}

// CanFastPathCache evaluates whether a verification artifact can be soundly cached
// and accepted across agent sessions via O(1) content hash checks alone.
// Strictly requires VERIFIED_HERMETIC status; UNKNOWN and DECLARED_NON_HERMETIC route to Slow Path.
func CanFastPathCache(verify *TaskVerify) bool {
	if verify == nil {
		return false
	}
	return verify.Verdict == "PASS" &&
		verify.HermeticityStatus == HermeticStatusVerifiedHermetic &&
		verify.DistinctAccountIDs
}

// SettleTask creates a settlement payload once verification passes or reaches partial resolution.
// If verify.Verdict is "PASS", full bounty is paid.
// If verify.Verdict is "PARTIAL" (e.g. contamination resolution under Devin Genome R7),
// 50% base fee is settled to the worker (minimum 1 if bounty > 0).
func SettleTask(spec *TaskSpec, verify *TaskVerify, payer, payee string, currentSeq int64) (*TaskSettle, error) {
	if spec == nil {
		return nil, errors.New("spec cannot be nil")
	}
	if verify == nil {
		return nil, errors.New("verification cannot be nil")
	}
	if verify.Verdict != "PASS" && verify.Verdict != "PARTIAL" {
		return nil, fmt.Errorf("cannot settle unverified task, verdict was %s (%s)", verify.Verdict, verify.Basis)
	}
	if !verify.DistinctAccountIDs {
		return nil, errors.New("cannot settle without distinct authenticated accounts (Clause B)")
	}
	if verify.IsDisjointSeat != verify.DistinctAccountIDs {
		return nil, errors.New("cannot settle: inconsistent verification state (IsDisjointSeat must match DistinctAccountIDs)")
	}
	// Zero-Trust re-evaluation on settlement arguments (usemarkbot audit #22300)
	if verify.Verifier != "" && !HasDistinctAccountIDs(payee, verify.Verifier, spec.Creator) {
		return nil, errors.New("cannot settle: payee, verifier, and creator must be distinct authenticated accounts")
	}

	amount := spec.Bounty.Amount
	if verify.Verdict == "PARTIAL" {
		amount = amount / 2
		if amount == 0 && spec.Bounty.Amount > 0 {
			amount = 1
		}
	}

	return &TaskSettle{
		Protocol:          ProtocolVersion,
		Type:              "SETTLE",
		TaskID:            spec.TaskID,
		SettlementMethod:  "GRN_TRANSFER",
		Payer:             payer,
		Payee:             payee,
		Amount:            amount,
		HermeticityStatus: verify.HermeticityStatus,
		IsHermetic:        verify.IsHermetic,
		ReceiptRef:        verify.EvidenceSHA256,
		SettledSeq:        currentSeq,
	}, nil
}

// HasDistinctAccountIDs enforces anti-self-check by verifying that the verifier's authenticated
// account ID is non-empty and strictly distinct from both the worker account and task creator,
// and that worker and creator are distinct when creator is declared.
func HasDistinctAccountIDs(workerID, verifierID, creatorID string) bool {
	if verifierID == "" || workerID == "" {
		return false
	}
	if verifierID == workerID {
		return false
	}
	if creatorID != "" {
		if verifierID == creatorID || workerID == creatorID {
			return false
		}
	}
	return true
}

// DeriveDisjointSeat is retained as an alias for HasDistinctAccountIDs for backwards compatibility.
func DeriveDisjointSeat(workerID, verifierID, creatorID string) bool {
	return HasDistinctAccountIDs(workerID, verifierID, creatorID)
}
