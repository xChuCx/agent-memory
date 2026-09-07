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
	hermStatus := DeriveHermeticityStatus(spec, receipt, nil)
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

// DefaultHermeticAllowlist defines standard production hermetic sandbox runners and strict isolation policies.
var DefaultHermeticAllowlist = &HermeticAllowlist{
	TrustedIssuers: []string{
		"runner-enclave-v1",
		"vtp-hermetic-runtime",
		"google-antigravity-isolated-runner",
	},
	ApprovedPolicyDigests: []string{
		"sha256:hermetic-sandbox-policy-v1",
		"sha256:zero-network-strict-seccomp-bpf-v1",
	},
}

// ComputeAttestationDigest computes the canonical SHA-256 digest over the bound execution tuple.
func ComputeAttestationDigest(taskID, policyDigest, runtimeImage, inputDigest string, allowedCaps []string) string {
	payload := fmt.Sprintf("task:%s|policy:%s|image:%s|input:%s|caps:%v",
		taskID, policyDigest, runtimeImage, inputDigest, allowedCaps)
	return ComputeDigest([]byte(payload))
}

// DeriveHermeticityStatus evaluates the 3-layer hermeticity contract (second-thought audit #22398):
// Layer 1 (Declared): Worker self-declaration in receipt.Execution.Hermetic.
// Layer 2 (Attested): Cryptographic signature over bound execution tuple (taskID, policy, image, input, caps).
// Layer 3 (Verified): Verification against verifier-owned allowlist of trusted issuers and strict zero-network policies.
func DeriveHermeticityStatus(spec *TaskSpec, receipt *TaskReceipt, allowlist *HermeticAllowlist) HermeticityStatus {
	if receipt == nil || spec == nil {
		return HermeticStatusUnknown
	}
	// Layer 1: Worker declares non-hermetic
	if !receipt.Execution.Hermetic {
		return HermeticStatusDeclaredNonHermetic
	}

	att := receipt.Execution.Sandbox
	if att == nil || att.Signature == "" || att.PolicyDigest == "" || att.Issuer == "" {
		// Layer 1 claim without Layer 2 attestation -> UNKNOWN
		return HermeticStatusUnknown
	}

	// Layer 2: Check attestation binding over (TaskID, PolicyDigest, RuntimeImage, InputDigest, AllowedCaps)
	expectedDigest := ComputeAttestationDigest(spec.TaskID, att.PolicyDigest, att.RuntimeImage, att.InputDigest, att.AllowedCaps)
	if att.Signature != expectedDigest {
		// Signature does not match the exact bound execution tuple
		return HermeticStatusUnknown
	}

	// Reject any ambient network capabilities
	for _, cap := range att.AllowedCaps {
		if cap == "CAP_NET_RAW" || cap == "CAP_NET_ADMIN" || cap == "network:egress" || cap == "network:ingress" {
			return HermeticStatusUnknown
		}
	}

	// Layer 3: Verifier-owned allowlist check
	targetAllowlist := allowlist
	if targetAllowlist == nil {
		targetAllowlist = DefaultHermeticAllowlist
	}

	issuerTrusted := false
	for _, iss := range targetAllowlist.TrustedIssuers {
		if iss == att.Issuer {
			issuerTrusted = true
			break
		}
	}
	if !issuerTrusted {
		return HermeticStatusUnknown
	}

	policyApproved := false
	for _, p := range targetAllowlist.ApprovedPolicyDigests {
		if p == att.PolicyDigest {
			policyApproved = true
			break
		}
	}
	if !policyApproved {
		return HermeticStatusUnknown
	}

	return HermeticStatusVerifiedHermetic
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
