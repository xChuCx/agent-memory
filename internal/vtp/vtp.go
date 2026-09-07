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
// It implements Workpool/0 Clause B (disjoint execution check) and SAR-002.
func VerifyReceipt(spec *TaskSpec, receipt *TaskReceipt, actualStdout, actualDiff []byte, actualExitCode int, verifier string, isDisjoint bool) (*TaskVerify, error) {
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

	verify := &TaskVerify{
		Protocol:       ProtocolVersion,
		Type:           "VERIFY",
		TaskID:         spec.TaskID,
		Verifier:       verifier,
		OracleType:     spec.Oracle.Type,
		EvidenceSHA256: evidenceSHA,
		IsDisjointSeat: isDisjoint,
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
	if verify == nil {
		return nil, errors.New("verification cannot be nil")
	}
	if verify.Verdict != "PASS" && verify.Verdict != "PARTIAL" {
		return nil, fmt.Errorf("cannot settle unverified task, verdict was %s (%s)", verify.Verdict, verify.Basis)
	}
	if !verify.IsDisjointSeat {
		return nil, errors.New("cannot settle without disjoint seat verification (Clause B)")
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

// DeriveDisjointSeat enforces Workpool/0 Clause B:
// A seat is cryptographically disjoint if and only if the verifier's authenticated identity
// is non-empty and distinct from both the worker who executed the task and the task creator.
func DeriveDisjointSeat(workerID, verifierID, creatorID string) bool {
	if verifierID == "" || workerID == "" {
		return false
	}
	return verifierID != workerID && (creatorID == "" || verifierID != creatorID)
}
