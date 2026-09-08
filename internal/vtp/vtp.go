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

// ComputeRawDigest returns the bit-exact SHA-256 hex string over unmodified raw bytes (SAR-007 Tier 1).
func ComputeRawDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ComputeDigest returns the canonical SHA-256 hex string with CRLF normalization (SAR-002 LF projection for diff/stdout).
func ComputeDigest(b []byte) string {
	normalized := NormalizeLF(b)
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

// ComputeLFDigest is an alias for ComputeDigest denoting the LF projection explicitly.
func ComputeLFDigest(b []byte) string {
	return ComputeDigest(b)
}

// DigestComparison reports the outcome of evaluating actual bytes against an expected digest
// under both bit-exact raw bytes and LF-normalized text projection (SAR-007, astranaut01 #23145).
type DigestComparison struct {
	RawDigest         string `json:"raw_digest"`
	LFDigest          string `json:"lf_digest"`
	RawEqual          bool   `json:"raw_equal"`
	LFProjectionEqual bool   `json:"lf_projection_equal"`
}

// CompareDigestProjections compares two byte slices under both bit-exact raw bytes
// and canonical LF-normalized text projection (SAR-007, astranaut01 #23145).
func CompareDigestProjections(a, b []byte) DigestComparison {
	rawA := ComputeRawDigest(a)
	rawB := ComputeRawDigest(b)
	lfA := ComputeDigest(a)
	lfB := ComputeDigest(b)
	return DigestComparison{
		RawDigest:         rawA,
		LFDigest:          lfA,
		RawEqual:          rawA == rawB,
		LFProjectionEqual: lfA == lfB,
	}
}

// CanonicalJSON serializes a value to canonical JSON conforming to RFC 8785 (JCS)
// with UTF-16 code unit key sorting, ECMAScript number serialization, and compact separators.
func CanonicalJSON(v any) ([]byte, error) {
	return CanonicalizeRFC8785(v)
}

// SpecHash returns the SHA-256 hex digest of the canonical JSON encoding of a TaskSpec (RFC 8785).
func SpecHash(spec *TaskSpec) (string, error) {
	b, err := CanonicalJSON(spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ReceiptHash returns the SHA-256 hex digest of the canonical JSON encoding of a TaskReceipt (RFC 8785).
func ReceiptHash(receipt *TaskReceipt) (string, error) {
	b, err := CanonicalJSON(receipt)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyHash returns the SHA-256 hex digest of the canonical JSON encoding of a TaskVerify (RFC 8785).
func VerifyHash(verify *TaskVerify) (string, error) {
	b, err := CanonicalJSON(verify)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyReceipt verifies a worker's TaskReceipt against live execution output and diffs.
// It automatically derives account distinctness (HasDistinctAccountIDs) from authenticated IDs
// and records operator independence as UNKNOWN unless backed by separate topological attestations.
func VerifyReceipt(spec *TaskSpec, receipt *TaskReceipt, actualStdout, actualDiff []byte, actualExitCode int, verifier string) (*TaskVerify, error) {
	return VerifyReceiptWithAllowlist(spec, receipt, actualStdout, actualDiff, actualExitCode, verifier, nil)
}

// VerifyReceiptWithAllowlist performs full dual-oracle verification with a caller-supplied verifier allowlist.
func VerifyReceiptWithAllowlist(spec *TaskSpec, receipt *TaskReceipt, actualStdout, actualDiff []byte, actualExitCode int, verifier string, allowlist *HermeticAllowlist) (*TaskVerify, error) {
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
	hermStatus, hermReason := DeriveHermeticityEvaluation(spec, receipt, allowlist)
	isHermetic := hermStatus == HermeticStatusVerifiedHermetic

	now := int64(0)
	if allowlist != nil && allowlist.CurrentTime > 0 {
		now = allowlist.CurrentTime
	} else {
		now = time.Now().Unix()
	}

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
		HermeticityReason:    hermReason,
		IsHermetic:           isHermetic,
		VerifiedAt:           now,
	}

	// Exit code checks
	if actualExitCode != 0 {
		verify.Verdict = "FAIL"
		verify.Basis = "NON_ZERO_EXIT"
		return verify, nil
	}
	if receipt.Execution.ExitCode != actualExitCode {
		verify.Verdict = "FAIL"
		verify.Basis = "EXIT_CODE_MISMATCH"
		return verify, nil
	}

	// Oracle assertions check: receipt executed assertions must satisfy declared spec assertions.
	// If identifiable AssertionResults are provided, each declared assertion must be reported passed.
	// Otherwise, fallback to verifying coverage count.
	if len(spec.Oracle.Assertions) > 0 {
		if len(receipt.Execution.AssertionResults) > 0 {
			passedMap := make(map[string]bool, len(receipt.Execution.AssertionResults))
			for _, ar := range receipt.Execution.AssertionResults {
				if ar.Passed {
					passedMap[ar.ID] = true
				}
			}
			for _, expected := range spec.Oracle.Assertions {
				if !passedMap[expected] {
					verify.Verdict = "FAIL"
					verify.Basis = "UNSATISFIED_ASSERTIONS"
					return verify, nil
				}
			}
		} else if receipt.Execution.ExecutedAssertions < len(spec.Oracle.Assertions) {
			verify.Verdict = "FAIL"
			verify.Basis = "UNSATISFIED_ASSERTIONS"
			return verify, nil
		}
	}

	// Commitments check: if receipt commits to a SpecSHA256, it must match canonical hash of spec
	if receipt.Commitments != nil && receipt.Commitments.SpecSHA256 != "" {
		specHash, err := SpecHash(spec)
		if err != nil {
			return nil, fmt.Errorf("canonical hash spec: %w", err)
		}
		if receipt.Commitments.SpecSHA256 != specHash {
			verify.Verdict = "FAIL"
			verify.Basis = "SPEC_COMMITMENT_MISMATCH"
			return verify, nil
		}
	}

	// Oracle digest requirements for deterministic oracle
	if spec.Oracle.Type == "DETERMINISTIC" && receipt.Execution.StdoutSHA256 == "" && receipt.Execution.DiffHunksSHA256 == "" {
		verify.Verdict = "FAIL"
		verify.Basis = "EMPTY_EXECUTION_DIGESTS"
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
	PublicKeys:  make(map[string]ed25519.PublicKey),
	KeyBindings: make(map[string]TrustedKeyBinding),
}

// RegisterKey adds or updates an ED25519 public key in the allowlist registry.
func (a *HermeticAllowlist) RegisterKey(keyID string, pubKey ed25519.PublicKey) {
	if a.PublicKeys == nil {
		a.PublicKeys = make(map[string]ed25519.PublicKey)
	}
	a.PublicKeys[keyID] = pubKey
}

// RegisterBoundKey binds an ED25519 public key to a specific issuer and runner ID (astranaut01 audit #22956).
func (a *HermeticAllowlist) RegisterBoundKey(keyID string, pubKey ed25519.PublicKey, issuer, runnerID string) {
	if a.KeyBindings == nil {
		a.KeyBindings = make(map[string]TrustedKeyBinding)
	}
	a.KeyBindings[keyID] = TrustedKeyBinding{
		PublicKey: pubKey,
		Issuer:    issuer,
		RunnerID:  runnerID,
	}
}

// RevokeKey marks a runner key as revoked (second-thought audit #22431).
func (a *HermeticAllowlist) RevokeKey(keyID string) {
	if a.RevokedKeyIDs == nil {
		a.RevokedKeyIDs = make(map[string]bool)
	}
	a.RevokedKeyIDs[keyID] = true
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
// under domain tag "VTP1-ATTEST-V2", guaranteeing unambiguous serialization with explicit runner and issuer binding
// (second-thought audit #22431; astranaut01 audit #22461, #22956).
func ComputeAttestationCanonicalBytes(taskID, issuer, runnerID, policyDigest string, policyEpoch uint64, runtimeImage, inputDigest, keyID string, allowedCaps []string, issuedAt, expiresAt int64) []byte {
	normCaps := NormalizeCapabilities(allowedCaps)
	var buf bytes.Buffer
	buf.WriteString("VTP1-ATTEST-V2\n")
	writeDelimited := func(name, val string) {
		fmt.Fprintf(&buf, "%s:%d:%s\n", name, len(val), val)
	}
	writeDelimited("task_id", taskID)
	writeDelimited("issuer", issuer)
	writeDelimited("runner_id", runnerID)
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
// Note: As of VTP-1.2, SHA-256 digests serve only as historical integrity evidence and CANNOT
// be used as proof of execution hermeticity without an ED25519 signature from a registered runner key
// (astranaut01 audit #22956).
func ComputeAttestationDigest(taskID, issuer, runnerID, policyDigest, runtimeImage, inputDigest, keyID string, allowedCaps []string, issuedAt, expiresAt int64) string {
	canonical := ComputeAttestationCanonicalBytes(taskID, issuer, runnerID, policyDigest, 1, runtimeImage, inputDigest, keyID, allowedCaps, issuedAt, expiresAt)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// DeriveHermeticityEvaluation evaluates the 3-layer hermeticity contract and returns both status and orthogonal reason code:
// Layer 1 (Declared): Worker self-declaration in receipt.Execution.Hermetic.
// Layer 2 (Attested): Cryptographic ED25519 signature over length-delimited canonical tuple bound to runner identity.
// Layer 3 (Verified): Strict allowlist check against verifier-owned key registry, policy allowlist, and temporal validity window.
// (second-thought audit #22345, #22398, #22431; astranaut01 audit #22461, #22956; just-nik audit #22960).
func DeriveHermeticityEvaluation(spec *TaskSpec, receipt *TaskReceipt, allowlist *HermeticAllowlist) (HermeticityStatus, HermeticityReason) {
	if receipt == nil || spec == nil {
		return HermeticStatusUnknown, HermeticReasonMissingSandbox
	}
	// Layer 1: Worker declares non-hermetic
	if !receipt.Execution.Hermetic {
		return HermeticStatusDeclaredNonHermetic, HermeticReasonDeclaredNonHermetic
	}

	att := receipt.Execution.Sandbox
	if att == nil {
		return HermeticStatusUnknown, HermeticReasonMissingSandbox
	}
	if att.Signature == "" {
		return HermeticStatusUnknown, HermeticReasonMissingSignature
	}
	if att.PolicyDigest == "" {
		return HermeticStatusUnknown, HermeticReasonMissingPolicyDigest
	}
	if att.Issuer == "" {
		return HermeticStatusUnknown, HermeticReasonMissingIssuer
	}

	// Canonicalize and reject any ambient network capabilities
	normCaps := NormalizeCapabilities(att.AllowedCaps)
	for _, cap := range normCaps {
		if cap == "cap_net_raw" || cap == "cap_net_admin" || cap == "network:egress" || cap == "network:ingress" || cap == "net:any" || strings.Contains(cap, "net") {
			return HermeticStatusUnknown, HermeticReasonNetworkCapabilityDenied
		}
	}

	// Layer 3: Verifier-owned allowlist check
	targetAllowlist := allowlist
	if targetAllowlist == nil {
		targetAllowlist = DefaultHermeticAllowlist
	}

	// Strict time window check (astranaut01 audit #22956)
	// Both IssuedAt and ExpiresAt must be strictly positive, and ExpiresAt must be strictly greater than IssuedAt.
	if att.IssuedAt <= 0 || att.ExpiresAt <= 0 || att.ExpiresAt <= att.IssuedAt {
		return HermeticStatusUnknown, HermeticReasonInvalidTimeWindow
	}

	now := targetAllowlist.CurrentTime
	if now == 0 {
		now = time.Now().Unix()
	}
	if now < att.IssuedAt {
		return HermeticStatusUnknown, HermeticReasonFutureIssuedAt
	}
	if now > att.ExpiresAt {
		return HermeticStatusUnknown, HermeticReasonExpired
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
		return HermeticStatusUnknown, HermeticReasonIssuerUntrusted
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
		return HermeticStatusUnknown, HermeticReasonPolicyUnapproved
	}

	// Check key revocation (second-thought audit #22431)
	if att.KeyID != "" && targetAllowlist.RevokedKeyIDs != nil && targetAllowlist.RevokedKeyIDs[att.KeyID] {
		return HermeticStatusUnknown, HermeticReasonKeyRevoked
	}

	// Check policy epoch (second-thought audit #22431)
	effectiveEpoch := att.PolicyEpoch
	if effectiveEpoch == 0 {
		effectiveEpoch = 1
	}
	if targetAllowlist.MinAcceptedEpoch > 0 && effectiveEpoch < targetAllowlist.MinAcceptedEpoch {
		return HermeticStatusUnknown, HermeticReasonEpochStale
	}

	// Layer 2 & 3 Key Verification: STRICT ED25519 ONLY. NO SHA-256 DOWNGRADE FALLBACK. (astranaut01 audit #22956)
	if len(targetAllowlist.PublicKeys) == 0 && len(targetAllowlist.KeyBindings) == 0 {
		return HermeticStatusUnknown, HermeticReasonMissingKeyRegistry
	}
	if att.KeyID == "" {
		return HermeticStatusUnknown, HermeticReasonKeyUnregistered
	}

	var pubKey ed25519.PublicKey
	if binding, ok := targetAllowlist.KeyBindings[att.KeyID]; ok {
		if binding.Issuer != "" && binding.Issuer != att.Issuer {
			return HermeticStatusUnknown, HermeticReasonKeyRunnerMismatch
		}
		if binding.RunnerID != "" && att.RunnerID != "" && binding.RunnerID != att.RunnerID {
			return HermeticStatusUnknown, HermeticReasonKeyRunnerMismatch
		}
		pubKey = binding.PublicKey
	} else if pk, ok := targetAllowlist.PublicKeys[att.KeyID]; ok {
		pubKey = pk
	} else {
		return HermeticStatusUnknown, HermeticReasonKeyUnregistered
	}

	if len(pubKey) != ed25519.PublicKeySize {
		return HermeticStatusUnknown, HermeticReasonKeyUnregistered
	}

	canonicalBytes := ComputeAttestationCanonicalBytes(spec.TaskID, att.Issuer, att.RunnerID, att.PolicyDigest, effectiveEpoch, att.RuntimeImage, att.InputDigest, att.KeyID, att.AllowedCaps, att.IssuedAt, att.ExpiresAt)

	sigBytes, err := hex.DecodeString(att.Signature)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return HermeticStatusUnknown, HermeticReasonSignatureInvalid
	}
	if !ed25519.Verify(pubKey, canonicalBytes, sigBytes) {
		return HermeticStatusUnknown, HermeticReasonSignatureInvalid
	}

	return HermeticStatusVerifiedHermetic, HermeticReasonVerified
}

// DeriveHermeticityStatus evaluates the 3-layer hermeticity contract, preserving backward-compatibility (second-thought audit #22398, #22431; astranaut01 #22461, #22956).
func DeriveHermeticityStatus(spec *TaskSpec, receipt *TaskReceipt, allowlist *HermeticAllowlist) HermeticityStatus {
	status, _ := DeriveHermeticityEvaluation(spec, receipt, allowlist)
	return status
}

// CanFastPathCache evaluates whether a verification artifact can be soundly cached
// and accepted across agent sessions via O(1) content hash checks alone.
// Strictly requires VERIFIED_HERMETIC status and distinct seats; UNKNOWN and DECLARED_NON_HERMETIC route to Slow Path.
func CanFastPathCache(verify *TaskVerify) bool {
	return CanFastPathCacheWithFreshness(verify, nil, "")
}

// CanFastPathCacheWithFreshness evaluates fast-path caching subject to runtime tenant access control
// and verification freshness limits (astranaut01 audit #22956).
// If a tenant's ACL is revoked, reuse is strictly rejected even if the prior verdict was PASS.
func CanFastPathCacheWithFreshness(verify *TaskVerify, freshness *FreshnessPolicy, tenantID string) bool {
	if verify == nil {
		return false
	}
	if verify.Verdict != "PASS" || verify.HermeticityStatus != HermeticStatusVerifiedHermetic || !verify.DistinctAccountIDs {
		return false
	}

	if freshness != nil {
		// Tenant ACL check: revoked or missing tenant access forbids reuse
		if freshness.TenantACL != nil && tenantID != "" {
			if !freshness.TenantACL[tenantID] {
				return false
			}
		}
		// Max-age freshness check
		if freshness.MaxAgeSeconds > 0 && verify.VerifiedAt > 0 {
			now := freshness.CurrentTime
			if now == 0 {
				now = time.Now().Unix()
			}
			if now < verify.VerifiedAt || (now-verify.VerifiedAt) > freshness.MaxAgeSeconds {
				return false
			}
		}
	}

	return true
}

// SettleTask creates a settlement payload once verification passes or reaches partial resolution.
// If verify.Verdict is "PASS", full bounty is paid.
// If verify.Verdict is "PARTIAL" (e.g. contamination resolution under Devin Genome R7),
// 50% base fee is settled to the worker (minimum 1 if bounty > 0).
func SettleTask(spec *TaskSpec, verify *TaskVerify, payer, payee string, currentSeq int64) (*TaskSettle, error) {
	return SettleTaskWithAllowlist(spec, verify, payer, payee, currentSeq, nil)
}

// SettleTaskWithAllowlist creates a settlement payload while validating hermetic execution gates,
// cross-task replay prevention, and verifier digital signature if an allowlist is provided.
func SettleTaskWithAllowlist(spec *TaskSpec, verify *TaskVerify, payer, payee string, currentSeq int64, allowlist *HermeticAllowlist) (*TaskSettle, error) {
	if spec == nil {
		return nil, errors.New("spec cannot be nil")
	}
	if verify == nil {
		return nil, errors.New("verification cannot be nil")
	}
	if verify.TaskID != spec.TaskID {
		return nil, fmt.Errorf("cannot settle task: verify TaskID %q does not match spec TaskID %q", verify.TaskID, spec.TaskID)
	}
	if verify.Verdict != "PASS" && verify.Verdict != "PARTIAL" {
		return nil, fmt.Errorf("cannot settle unverified task, verdict was %s (%s)", verify.Verdict, verify.Basis)
	}
	// Hermetic Oracle Settlement Gate: If spec requires hermetic execution, verification MUST be VERIFIED_HERMETIC.
	if spec.Oracle.Hermetic && (verify.HermeticityStatus != HermeticStatusVerifiedHermetic || !verify.IsHermetic) {
		return nil, fmt.Errorf("cannot settle task: spec requires hermetic execution, but verification hermeticity status was %s (%s)", verify.HermeticityStatus, verify.HermeticityReason)
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

	// Verifier authentication check if allowlist has registered public keys
	if allowlist != nil && len(allowlist.PublicKeys) > 0 {
		if verify.VerifierKeyID == "" || verify.Signature == "" {
			return nil, errors.New("cannot settle: verification lacks verifier signature under configured allowlist")
		}
		pubKey, ok := allowlist.PublicKeys[verify.VerifierKeyID]
		if !ok {
			return nil, fmt.Errorf("cannot settle: verifier key %q not found in allowlist", verify.VerifierKeyID)
		}
		valid, err := VerifyTaskVerifySignature(verify, pubKey)
		if err != nil || !valid {
			return nil, fmt.Errorf("cannot settle: verifier signature verification failed: %v", err)
		}
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

// SignTaskVerify computes the canonical RFC 8785 JSON of TaskVerify (with Signature and VerifierKeyID excluded)
// and attaches the ED25519 hex signature.
func SignTaskVerify(verify *TaskVerify, privKey ed25519.PrivateKey, keyID string) error {
	if verify == nil {
		return errors.New("verify cannot be nil")
	}
	clone := *verify
	clone.Signature = ""
	clone.VerifierKeyID = ""
	b, err := CanonicalJSON(&clone)
	if err != nil {
		return err
	}
	sig := ed25519.Sign(privKey, b)
	verify.Signature = hex.EncodeToString(sig)
	verify.VerifierKeyID = keyID
	return nil
}

// VerifyTaskVerifySignature verifies the ED25519 signature on a TaskVerify against canonical bytes.
func VerifyTaskVerifySignature(verify *TaskVerify, pubKey ed25519.PublicKey) (bool, error) {
	if verify == nil {
		return false, errors.New("verify cannot be nil")
	}
	if verify.Signature == "" {
		return false, errors.New("verify has no signature")
	}
	sigBytes, err := hex.DecodeString(verify.Signature)
	if err != nil {
		return false, fmt.Errorf("invalid hex signature: %w", err)
	}
	clone := *verify
	clone.Signature = ""
	clone.VerifierKeyID = ""
	b, err := CanonicalJSON(&clone)
	if err != nil {
		return false, err
	}
	return ed25519.Verify(pubKey, b, sigBytes), nil
}

// HasDistinctAccountIDs evaluates the string identifiers of worker, verifier, and creator.
//
// SECURITY NOTICE (Clause B - Experimental):
// String inequality is an anti-self-check heuristic, NOT a cryptographic identity or process-isolation
// proof. In environments without external PKI / authenticated account infrastructure, separate string
// handles from the same operator will satisfy this check. True operator independence requires cryptographic
// identity attestations (e.g. Ed25519 account signatures or TPM/enclave attestations).
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
