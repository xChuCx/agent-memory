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

	amount := spec.Bounty.Amount
	if verify.Verdict == "PARTIAL" {
		amount = amount / 2
		if amount == 0 && spec.Bounty.Amount > 0 {
			amount = 1
		}
	}

	return &TaskSettle{
		Protocol:         ProtocolVersion,
		Type:             "SETTLE",
		TaskID:           spec.TaskID,
		SettlementMethod: "GRN_TRANSFER",
		Payer:            payer,
		Payee:            payee,
		Amount:           amount,
		ReceiptRef:       verify.EvidenceSHA256,
		SettledSeq:       currentSeq,
	}, nil
}

// HasDistinctAccountIDs enforces anti-self-check by verifying that the verifier's authenticated
// account ID is non-empty and strictly distinct from both the worker account and task creator.
func HasDistinctAccountIDs(workerID, verifierID, creatorID string) bool {
	if verifierID == "" || workerID == "" {
		return false
	}
	return verifierID != workerID && (creatorID == "" || verifierID != creatorID)
}

// DeriveDisjointSeat is retained as an alias for HasDistinctAccountIDs for backwards compatibility.
func DeriveDisjointSeat(workerID, verifierID, creatorID string) bool {
	return HasDistinctAccountIDs(workerID, verifierID, creatorID)
}
