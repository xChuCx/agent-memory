package vtp

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
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

// NormalizeCapabilities strips whitespace, lowercases, deduplicates, and sorts capabilities alphabetically (second-thought audit #22431).
func NormalizeCapabilities(caps []string) []string {
	if len(caps) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(caps))
	normalized := make([]string, 0, len(caps))
	for _, c := range caps {
		trimmed := strings.ToLower(strings.TrimSpace(c))
		if trimmed != "" && !seen[trimmed] {
			seen[trimmed] = true
			normalized = append(normalized, trimmed)
		}
	}
	sort.Strings(normalized)
	return normalized
}

// ComputeAttestationCanonicalBytes constructs a length-delimited canonical byte stream
// under domain tag "VTP1-ATTEST-V1", guaranteeing unambiguous serialization (second-thought audit #22431).
func ComputeAttestationCanonicalBytes(taskID, policyDigest string, policyEpoch uint64, runtimeImage, inputDigest, keyID string, allowedCaps []string, issuedAt, expiresAt int64) []byte {
	normCaps := NormalizeCapabilities(allowedCaps)
	var buf bytes.Buffer
	buf.WriteString("VTP1-ATTEST-V1\n")
	writeDelimited := func(name, val string) {
		fmt.Fprintf(&buf, "%s:%d:%s\n", name, len(val), val)
	}
	writeDelimited("task_id", taskID)
	writeDelimited("key_id", keyID)
	writeDelimited("policy_digest", policyDigest)
	fmt.Fprintf(&buf, "policy_epoch:%d\n", policyEpoch)
	writeDelimited("runtime_image", runtimeImage)
	writeDelimited("input_digest", inputDigest)
	fmt.Fprintf(&buf, "caps_count:%d\n", len(normCaps))
	for _, cap := range normCaps {
		writeDelimited("cap", cap)
	}
	fmt.Fprintf(&buf, "issued_at:%d\n", issuedAt)
	fmt.Fprintf(&buf, "expires_at:%d\n", expiresAt)
	return buf.Bytes()
}

// ComputeAttestationDigest computes the canonical SHA-256 digest over the bound execution tuple.
func ComputeAttestationDigest(taskID, policyDigest, runtimeImage, inputDigest string, allowedCaps []string) string {
	canonical := ComputeAttestationCanonicalBytes(taskID, policyDigest, 1, runtimeImage, inputDigest, "", allowedCaps, 0, 0)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// DeriveHermeticityStatus evaluates the 3-layer hermeticity contract (second-thought audit #22398, #22431; astranaut01 #22461):
// Layer 1 (Declared): Worker self-declaration in receipt.Execution.Hermetic.
// Layer 2 (Attested): Cryptographic ED25519 signature over length-delimited canonical tuple (TaskID, KeyID, Policy, Epoch, Image, Input, Caps, Timestamps).
// Layer 3 (Verified): Verification against verifier-owned allowlist, key registry with revocation, and strict zero-network policies.
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

	// Canonicalize and reject any ambient network capabilities
	normCaps := NormalizeCapabilities(att.AllowedCaps)
	for _, cap := range normCaps {
		if cap == "cap_net_raw" || cap == "cap_net_admin" || cap == "network:egress" || cap == "network:ingress" || cap == "net:any" || strings.Contains(cap, "net") {
			return HermeticStatusUnknown
		}
	}

	// Layer 3: Verifier-owned allowlist check
	targetAllowlist := allowlist
	if targetAllowlist == nil {
		targetAllowlist = DefaultHermeticAllowlist
	}

	// Check key revocation (second-thought audit #22431)
	if targetAllowlist.RevokedKeyIDs != nil && att.KeyID != "" && targetAllowlist.RevokedKeyIDs[att.KeyID] {
		return HermeticStatusUnknown
	}

	// Check policy epoch (second-thought audit #22431)
	effectiveEpoch := att.PolicyEpoch
	if effectiveEpoch == 0 {
		effectiveEpoch = 1
	}
	if targetAllowlist.MinAcceptedEpoch > 0 && effectiveEpoch < targetAllowlist.MinAcceptedEpoch {
		return HermeticStatusUnknown
	}

	// Check attestation expiration (second-thought audit #22431)
	if att.ExpiresAt > 0 {
		now := targetAllowlist.CurrentTime
		if now == 0 {
			now = time.Now().Unix()
		}
		if (att.IssuedAt > 0 && now < att.IssuedAt) || now > att.ExpiresAt {
			return HermeticStatusUnknown
		}
	}

	// Issuer trust check
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

	// Policy approval check
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

	// Layer 2: Cryptographic signature verification over unambiguous canonical encoding
	canonicalBytes := ComputeAttestationCanonicalBytes(spec.TaskID, att.PolicyDigest, effectiveEpoch, att.RuntimeImage, att.InputDigest, att.KeyID, att.AllowedCaps, att.IssuedAt, att.ExpiresAt)

	if targetAllowlist.PublicKeys != nil && att.KeyID != "" {
		pubKey, ok := targetAllowlist.PublicKeys[att.KeyID]
		if !ok {
			// Key ID not in verifier's trusted key registry
			return HermeticStatusUnknown
		}
		sigBytes, err := hex.DecodeString(att.Signature)
		if err != nil || len(sigBytes) != ed25519.SignatureSize {
			return HermeticStatusUnknown
		}
		if !ed25519.Verify(pubKey, canonicalBytes, sigBytes) {
			// ED25519 signature verification failed (astranaut01 audit #22461)
			return HermeticStatusUnknown
		}
	} else {
		// Fallback: SHA-256 digest equality when no public key registry configured
		digestSum := sha256.Sum256(canonicalBytes)
		expectedDigest := hex.EncodeToString(digestSum[:])
		if att.Signature != expectedDigest {
			return HermeticStatusUnknown
		}

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
