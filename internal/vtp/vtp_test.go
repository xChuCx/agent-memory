package vtp

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)


func TestVTP_FullLifecycle(t *testing.T) {
	// 1. Task Spec
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-test-001",
		Title:    "Test verification of invariant",
		Bounty: BountySpec{
			Currency: "GRN",
			Amount:   1,
		},
		Oracle: OracleSpec{
			Type:       "execution@1",
			Target:     "pkg/verify",
			Assertions: []string{"exit_code == 0", "stdout_sha256 == valid"},
		},
		Context: ContextSpec{
			Repository: "https://github.com/xChuCx/agent-memory",
			BaseCommit: "7ef762a",
		},
	}

	// 2. Task Claim
	claim := &TaskClaim{
		Protocol:       ProtocolVersion,
		Type:           "CLAIM",
		TaskID:         spec.TaskID,
		Worker:         "antigravity-wanderer",
		WorkerID:       "63d0b4fd-f412-4db0-b81c-93a05cbc3a6a",
		ClaimedSeq:     14400,
		TTLSeq:         14500,
		IdempotencyKey: "vtp-claim-001",
	}
	if claim.TaskID != spec.TaskID {
		t.Fatalf("claim task ID mismatch")
	}

	// Simulated execution outputs
	mockStdout := []byte("PASS: 20 ok, 0 FAIL\r\n") // Note CRLF
	mockDiff := []byte("diff --git a/pkg.go b/pkg.go\n+func Verify() {}\n")

	// Calculate digests (with CRLF normalization per SAR-002)
	stdoutDigest := ComputeDigest(mockStdout)
	diffDigest := ComputeDigest(mockDiff)

	// 3. Task Receipt
	receipt := &TaskReceipt{
		Protocol: ProtocolVersion,
		Type:     "RECEIPT",
		TaskID:   spec.TaskID,
		Worker:   claim.Worker,
		Execution: ExecutionReceipt{
			StdoutSHA256:       stdoutDigest,
			DiffHunksSHA256:    diffDigest,
			ExecutedAssertions: 2,
			AssertionResults: []AssertionResult{
				{ID: "exit_code == 0", Passed: true, Evidence: "exit 0"},
				{ID: "stdout_sha256 == valid", Passed: true, Evidence: stdoutDigest},
			},
			ExitCode: 0,
		},
		Artifacts:      []string{"pkg.go"},
		IdempotencyKey: "vtp-receipt-001",
	}

	// 4. Verification (Derived Account Distinctness)
	verify, err := VerifyReceipt(spec, receipt, mockStdout, mockDiff, 0, "orca-agent")
	if err != nil {
		t.Fatalf("unexpected verification error: %v", err)
	}
	if verify.Verdict != "PASS" {
		t.Fatalf("expected PASS, got %s (basis: %s)", verify.Verdict, verify.Basis)
	}
	if !verify.DistinctAccountIDs || !verify.IsDisjointSeat {
		t.Fatalf("expected distinct account IDs flag")
	}
	if verify.OperatorIndependence != "UNKNOWN" {
		t.Fatalf("expected UNKNOWN operator independence without topological attestation, got %s", verify.OperatorIndependence)
	}

	// 5. Settlement
	settle, err := SettleTask(spec, verify, "payer-node", claim.Worker, 14450)
	if err != nil {
		t.Fatalf("unexpected settlement error: %v", err)
	}
	if settle.Amount != 1 || settle.SettlementMethod != "GRN_TRANSFER" {
		t.Fatalf("invalid settlement payload: %+v", settle)
	}
}

func TestVTP_Falsifiers(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-falsify",
		Creator:  "task-creator",
		Bounty:   BountySpec{Currency: "GRN", Amount: 1},
	}
	receipt := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-seat",
		Execution: ExecutionReceipt{
			StdoutSHA256: "expected_hash",
			ExitCode:     0,
		},
		IdempotencyKey: "rec-001",
	}

	// Negative control 1: Non-zero exit code
	v1, err := VerifyReceipt(spec, receipt, []byte("fail"), []byte("diff"), 1, "verifier")
	if err != nil || v1.Verdict != "FAIL" || v1.Basis != "NON_ZERO_EXIT" {
		t.Fatalf("expected NON_ZERO_EXIT failure, got %+v", v1)
	}

	// Negative control 2: Digest mismatch
	v2, err := VerifyReceipt(spec, receipt, []byte("tampered output"), []byte("diff"), 0, "verifier")
	if err != nil || v2.Verdict != "FAIL" || v2.Basis != "STDOUT_DIGEST_MISMATCH" {
		t.Fatalf("expected STDOUT_DIGEST_MISMATCH failure, got %+v", v2)
	}

	// Negative control 3: Literal self-verification attempt (anti-self-check)
	vSelf, err := VerifyReceipt(spec, receipt, []byte("expected_hash"), []byte(""), 0, receipt.Worker)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vSelf.DistinctAccountIDs {
		t.Fatalf("expected DistinctAccountIDs to be false when verifier == worker")
	}

	// Negative control 4: Exit code mismatch (receipt claims 1, verifier passes 0)
	recExitMismatch := *receipt
	recExitMismatch.Execution.ExitCode = 1
	vExitMismatch, err := VerifyReceipt(spec, &recExitMismatch, []byte("expected_hash"), []byte(""), 0, "verifier")
	if err != nil || vExitMismatch.Verdict != "FAIL" || vExitMismatch.Basis != "EXIT_CODE_MISMATCH" {
		t.Fatalf("expected EXIT_CODE_MISMATCH, got %+v", vExitMismatch)
	}

	// Negative control 5: Unsatisfied assertions
	specWithAssertions := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-assertions",
		Creator:  "task-creator",
		Oracle: OracleSpec{
			Assertions: []string{"check1", "check2"},
		},
	}
	recUnsatisfied := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   specWithAssertions.TaskID,
		Worker:   "worker-seat",
		Execution: ExecutionReceipt{
			ExitCode:           0,
			ExecutedAssertions: 1, // only 1 of 2
		},
		IdempotencyKey: "rec-unsatisfied",
	}
	vUnsatisfied, err := VerifyReceipt(specWithAssertions, recUnsatisfied, []byte(""), []byte(""), 0, "verifier")
	if err != nil || vUnsatisfied.Verdict != "FAIL" || vUnsatisfied.Basis != "UNSATISFIED_ASSERTIONS" {
		t.Fatalf("expected UNSATISFIED_ASSERTIONS, got %+v", vUnsatisfied)
	}

	// Negative control 6: Empty digests on deterministic oracle
	specDeterministic := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-det",
		Creator:  "task-creator",
		Oracle: OracleSpec{
			Type: "DETERMINISTIC",
		},
	}
	recEmptyDigests := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   specDeterministic.TaskID,
		Worker:   "worker-seat",
		Execution: ExecutionReceipt{
			ExitCode: 0,
		},
		IdempotencyKey: "rec-det",
	}
	vEmptyDigests, err := VerifyReceipt(specDeterministic, recEmptyDigests, []byte(""), []byte(""), 0, "verifier")
	if err != nil || vEmptyDigests.Verdict != "FAIL" || vEmptyDigests.Basis != "EMPTY_EXECUTION_DIGESTS" {
		t.Fatalf("expected EMPTY_EXECUTION_DIGESTS, got %+v", vEmptyDigests)
	}
	_, err = SettleTask(spec, vSelf, "payer", "payee", 14450)
	if err == nil {
		t.Fatalf("expected settlement failure when DistinctAccountIDs is false")
	}

	// Negative control 4: Creator self-verification attempt
	vCreator, err := VerifyReceipt(spec, receipt, []byte("expected_hash"), []byte(""), 0, spec.Creator)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vCreator.DistinctAccountIDs {
		t.Fatalf("expected DistinctAccountIDs to be false when verifier == creator")
	}
	_, err = SettleTask(spec, vCreator, "payer", "payee", 14450)
	if err == nil {
		t.Fatalf("expected settlement failure when verifier is task creator")
	}
}

func TestVTP_PartialSettlement(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-partial",
		Bounty:   BountySpec{Currency: "GRN", Amount: 10},
	}
	verify := &TaskVerify{
		Protocol:           ProtocolVersion,
		Type:               "VERIFY",
		TaskID:             spec.TaskID,
		Verifier:           "verifier-node",
		Verdict:            "PARTIAL",
		Basis:              "CONTAMINATION_GAME_THEORY",
		EvidenceSHA256:     "evidence_partial_hash",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
	}

	settle, err := SettleTask(spec, verify, "payer-node", "worker-node", 15000)
	if err != nil {
		t.Fatalf("unexpected error on partial settlement: %v", err)
	}
	if settle.Amount != 5 {
		t.Fatalf("expected 50%% payout (5 GRN), got %d", settle.Amount)
	}

	// Boundary case: bounty is 1 GRN, 50% rounds up to minimum 1
	specMin := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-partial-1",
		Bounty:   BountySpec{Currency: "GRN", Amount: 1},
	}
	verifyMin := &TaskVerify{
		Protocol:           ProtocolVersion,
		Type:               "VERIFY",
		TaskID:             specMin.TaskID,
		Verifier:           "verifier-node",
		Verdict:            "PARTIAL",
		Basis:              "FACT_INCONSISTENT_CONTAMINATION",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
	}
	settleMin, err := SettleTask(specMin, verifyMin, "payer-node", "worker-node", 15001)
	if err != nil {
		t.Fatalf("unexpected error on minimum partial settlement: %v", err)
	}
	if settleMin.Amount != 1 {
		t.Fatalf("expected minimum 1 GRN payout, got %d", settleMin.Amount)
	}
}

func TestVTP_DeriveDisjointSeat(t *testing.T) {
	cases := []struct {
		name     string
		worker   string
		verifier string
		creator  string
		expected bool
	}{
		{"disjoint valid", "worker-01", "verifier-02", "creator-00", true},
		{"self-verification forbidden", "worker-01", "worker-01", "creator-00", false},
		{"creator cannot self-verify", "worker-01", "creator-00", "creator-00", false},
		{"worker cannot be creator", "creator-00", "verifier-02", "creator-00", false},
		{"empty verifier invalid", "worker-01", "", "creator-00", false},
		{"empty worker invalid", "", "verifier-02", "creator-00", false},
		{"creator optional but distinct", "worker-01", "verifier-02", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := DeriveDisjointSeat(tc.worker, tc.verifier, tc.creator)
			if res != tc.expected {
				t.Fatalf("%s: expected %v, got %v", tc.name, tc.expected, res)
			}
		})
	}
}

func TestVTP_SettlementInconsistencyBypass(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-bypass",
		Bounty:   BountySpec{Currency: "GRN", Amount: 10},
	}

	cases := []struct {
		name               string
		distinctAccountIDs bool
		isDisjointSeat     bool
		shouldPass         bool
	}{
		{
			name:               "switchboard bypass attempt (distinct=false, disjoint=true)",
			distinctAccountIDs: false,
			isDisjointSeat:     true,
			shouldPass:         false,
		},
		{
			name:               "both false",
			distinctAccountIDs: false,
			isDisjointSeat:     false,
			shouldPass:         false,
		},
		{
			name:               "inconsistent state (distinct=true, disjoint=false)",
			distinctAccountIDs: true,
			isDisjointSeat:     false,
			shouldPass:         false,
		},
		{
			name:               "consistent valid state (distinct=true, disjoint=true)",
			distinctAccountIDs: true,
			isDisjointSeat:     true,
			shouldPass:         true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verify := &TaskVerify{
				Protocol:           ProtocolVersion,
				Type:               "VERIFY",
				TaskID:             spec.TaskID,
				Verifier:           "verifier-01",
				Verdict:            "PASS",
				Basis:              "FACT_CONSISTENT",
				DistinctAccountIDs: tc.distinctAccountIDs,
				IsDisjointSeat:     tc.isDisjointSeat,
			}
			settle, err := SettleTask(spec, verify, "payer-01", "worker-01", 20000)
			if tc.shouldPass {
				if err != nil {
					t.Fatalf("expected settlement to pass, got: %v", err)
				}
				if settle == nil || settle.Amount != 10 {
					t.Fatalf("expected settlement amount 10, got: %v", settle)
				}
			} else {
				if err == nil {
					t.Fatalf("expected settlement to FAIL, but passed: %v", settle)
				}
			}
		})
	}
}

func TestVTP_HermeticityFastPathGating(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-fastpath-gate",
		Creator:  "creator-node",
		Oracle:   OracleSpec{Type: "execution@1", Hermetic: true},
	}

	trustedIssuer := "runner-enclave-v1"
	approvedPolicy := "sha256:hermetic-sandbox-policy-v1"
	validImage := "docker.io/library/alpine@sha256:e1c0d"
	validInput := "sha256:input-files-v1"
	allowedCaps := []string{"CAP_SYS_RESOURCE"}
	runnerID := "enclave-runner-node-01"
	keyID := "runner-key-ed25519-v1"

	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{trustedIssuer},
		ApprovedPolicyDigests: []string{approvedPolicy},
		CurrentEpoch:          1,
		MinAcceptedEpoch:      1,
		CurrentTime:           1500,
	}
	allowlist.RegisterBoundKey(keyID, pubKey, trustedIssuer, runnerID)

	issuedAt := int64(1000)
	expiresAt := int64(2000)

	canonicalBytes := ComputeAttestationCanonicalBytes(
		spec.TaskID, trustedIssuer, runnerID, approvedPolicy, 1, validImage, validInput, keyID, allowedCaps, issuedAt, expiresAt,
	)
	validSig := hex.EncodeToString(ed25519.Sign(privKey, canonicalBytes))

	// Case 1: Genuine ED25519 attestation + registered key + allowlisted policy -> VERIFIED_HERMETIC & CanFastPathCache=true
	recVerified := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    validSig,
			},
		},
	}
	vVerified, err := VerifyReceiptWithAllowlist(spec, recVerified, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vVerified.HermeticityStatus != HermeticStatusVerifiedHermetic || !vVerified.IsHermetic {
		t.Fatalf("expected VERIFIED_HERMETIC, got %s", vVerified.HermeticityStatus)
	}
	if vVerified.HermeticityReason != HermeticReasonVerified {
		t.Fatalf("expected REASON_VERIFIED, got %s", vVerified.HermeticityReason)
	}
	if !CanFastPathCache(vVerified) {
		t.Fatalf("expected CanFastPathCache=true for VERIFIED_HERMETIC PASS")
	}

	// Case 2: P1 Regression - Attacker recomputes SHA-256 hash without private key -> MUST FAIL
	// (astranaut01 audit #22956: fallback accepting public hash as signature must be eliminated)
	shaDigest := sha256.Sum256(canonicalBytes)
	forgedSHASig := hex.EncodeToString(shaDigest[:])
	recForgedSHA := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    forgedSHASig, // SHA-256 hash instead of ED25519 signature
			},
		},
	}
	vForgedSHA, err := VerifyReceiptWithAllowlist(spec, recForgedSHA, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vForgedSHA.HermeticityStatus != HermeticStatusUnknown || vForgedSHA.IsHermetic {
		t.Fatalf("CRITICAL SECURITY FLAW: recomputed SHA-256 hash was accepted as VERIFIED_HERMETIC! got: %s", vForgedSHA.HermeticityStatus)
	}
	if vForgedSHA.HermeticityReason != HermeticReasonSignatureInvalid {
		t.Fatalf("expected REASON_SIGNATURE_INVALID for forged SHA, got %s", vForgedSHA.HermeticityReason)
	}
	if CanFastPathCache(vForgedSHA) {
		t.Fatalf("CRITICAL SECURITY FLAW: CanFastPathCache accepted forged SHA attestation!")
	}

	// Case 3: Empty KeyID bypass attempt -> MUST FAIL
	recEmptyKey := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        "", // empty KeyID
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    validSig,
			},
		},
	}
	vEmptyKey, err := VerifyReceiptWithAllowlist(spec, recEmptyKey, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vEmptyKey.HermeticityStatus != HermeticStatusUnknown || vEmptyKey.HermeticityReason != HermeticReasonKeyUnregistered {
		t.Fatalf("expected UNKNOWN with REASON_KEY_UNREGISTERED for empty KeyID, got %s (%s)", vEmptyKey.HermeticityStatus, vEmptyKey.HermeticityReason)
	}
	if CanFastPathCache(vEmptyKey) {
		t.Fatalf("expected CanFastPathCache=false for empty KeyID")
	}

	// Case 4: Missing Key Registry (verifier has nil/empty key registry) -> MUST FAIL with REASON_MISSING_KEY_REGISTRY
	emptyAllowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{trustedIssuer},
		ApprovedPolicyDigests: []string{approvedPolicy},
		CurrentTime:           1500,
	}
	vNoRegistry, err := VerifyReceiptWithAllowlist(spec, recVerified, []byte(""), []byte(""), 0, "verifier-node", emptyAllowlist)
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vNoRegistry.HermeticityStatus != HermeticStatusUnknown || vNoRegistry.HermeticityReason != HermeticReasonMissingKeyRegistry {
		t.Fatalf("expected UNKNOWN with REASON_MISSING_KEY_REGISTRY, got %s (%s)", vNoRegistry.HermeticityStatus, vNoRegistry.HermeticityReason)
	}
	if CanFastPathCache(vNoRegistry) {
		t.Fatalf("expected CanFastPathCache=false when verifier has no key registry")
	}

	// Case 5: Unregistered KeyID -> REASON_KEY_UNREGISTERED
	recUnregisteredKey := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        "unknown-rogue-key-id",
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    validSig,
			},
		},
	}
	vUnregistered, _ := VerifyReceiptWithAllowlist(spec, recUnregisteredKey, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if vUnregistered.HermeticityStatus != HermeticStatusUnknown || vUnregistered.HermeticityReason != HermeticReasonKeyUnregistered {
		t.Fatalf("expected REASON_KEY_UNREGISTERED, got %s (%s)", vUnregistered.HermeticityStatus, vUnregistered.HermeticityReason)
	}

	// Case 6: Key/Runner Mismatch (key is bound to runnerID, attestation claims another runner) -> REASON_KEY_RUNNER_MISMATCH
	recRunnerMismatch := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     "impostor-runner-99", // Key is bound to enclave-runner-node-01!
				KeyID:        keyID,
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    validSig,
			},
		},
	}
	vRunnerMismatch, _ := VerifyReceiptWithAllowlist(spec, recRunnerMismatch, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if vRunnerMismatch.HermeticityStatus != HermeticStatusUnknown || vRunnerMismatch.HermeticityReason != HermeticReasonKeyRunnerMismatch {
		t.Fatalf("expected REASON_KEY_RUNNER_MISMATCH, got %s (%s)", vRunnerMismatch.HermeticityStatus, vRunnerMismatch.HermeticityReason)
	}

	// Case 7: Missing/Zero Expiry boundary (astranaut01 audit #22956: missing expiry must not bypass check)
	recZeroExpiry := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    0, // zero expiration
				Signature:    validSig,
			},
		},
	}
	vZeroExpiry, _ := VerifyReceiptWithAllowlist(spec, recZeroExpiry, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if vZeroExpiry.HermeticityStatus != HermeticStatusUnknown || vZeroExpiry.HermeticityReason != HermeticReasonInvalidTimeWindow {
		t.Fatalf("expected REASON_INVALID_TIME_WINDOW for zero expiry, got %s (%s)", vZeroExpiry.HermeticityStatus, vZeroExpiry.HermeticityReason)
	}

	// Case 8: Inverted Time Window (ExpiresAt <= IssuedAt)
	recInvertedTime := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     2000,
				ExpiresAt:    1000, // precedes issuance!
				Signature:    validSig,
			},
		},
	}
	vInverted, _ := VerifyReceiptWithAllowlist(spec, recInvertedTime, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if vInverted.HermeticityStatus != HermeticStatusUnknown || vInverted.HermeticityReason != HermeticReasonInvalidTimeWindow {
		t.Fatalf("expected REASON_INVALID_TIME_WINDOW for inverted time, got %s (%s)", vInverted.HermeticityStatus, vInverted.HermeticityReason)
	}

	// Case 9: Ambient Network Capabilities -> REASON_NETWORK_CAPABILITY_DENIED
	recNetCap := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: approvedPolicy,
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  []string{"cap_chown", "network:egress"},
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    validSig,
			},
		},
	}
	vNetCap, _ := VerifyReceiptWithAllowlist(spec, recNetCap, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if vNetCap.HermeticityStatus != HermeticStatusUnknown || vNetCap.HermeticityReason != HermeticReasonNetworkCapabilityDenied {
		t.Fatalf("expected REASON_NETWORK_CAPABILITY_DENIED, got %s (%s)", vNetCap.HermeticityStatus, vNetCap.HermeticityReason)
	}

	// Case 10: Unapproved Policy Digest -> REASON_POLICY_UNAPPROVED
	recBogusPolicy := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: "sha256:arbitrary-unapproved-policy",
				PolicyEpoch:  1,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				IssuedAt:     issuedAt,
				ExpiresAt:    expiresAt,
				Signature:    validSig,
			},
		},
	}
	vBogus, _ := VerifyReceiptWithAllowlist(spec, recBogusPolicy, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if vBogus.HermeticityStatus != HermeticStatusUnknown || vBogus.HermeticityReason != HermeticReasonPolicyUnapproved {
		t.Fatalf("expected REASON_POLICY_UNAPPROVED, got %s (%s)", vBogus.HermeticityStatus, vBogus.HermeticityReason)
	}

	// Case 11: Declared Non-hermetic execution receipt -> DECLARED_NON_HERMETIC
	recNonHermetic := &TaskReceipt{
		Protocol:  ProtocolVersion,
		TaskID:    spec.TaskID,
		Worker:    "worker-node",
		Execution: ExecutionReceipt{ExitCode: 0, Hermetic: false},
	}
	vNonHermetic, err := VerifyReceiptWithAllowlist(spec, recNonHermetic, []byte(""), []byte(""), 0, "verifier-node", allowlist)
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vNonHermetic.HermeticityStatus != HermeticStatusDeclaredNonHermetic || vNonHermetic.HermeticityReason != HermeticReasonDeclaredNonHermetic {
		t.Fatalf("expected DECLARED_NON_HERMETIC, got %s (%s)", vNonHermetic.HermeticityStatus, vNonHermetic.HermeticityReason)
	}
	if CanFastPathCache(vNonHermetic) {
		t.Fatalf("expected CanFastPathCache=FALSE for DECLARED_NON_HERMETIC")
	}

	// Case 12: Tenant ACL Revocation on Cache Reuse (astranaut01 audit #22956)
	// Even though vVerified was PASS and VERIFIED_HERMETIC, if tenant's ACL is revoked, reuse MUST be denied.
	activeFreshness := &FreshnessPolicy{
		MaxAgeSeconds: 3600,
		CurrentTime:   1600,
		TenantACL:     map[string]bool{"tenant-authorized": true, "tenant-revoked": false},
	}
	if !CanFastPathCacheWithFreshness(vVerified, activeFreshness, "tenant-authorized") {
		t.Fatalf("expected CanFastPathCacheWithFreshness=true for authorized tenant")
	}
	if CanFastPathCacheWithFreshness(vVerified, activeFreshness, "tenant-revoked") {
		t.Fatalf("SECURITY VIOLATION: revoked tenant was permitted to reuse fast-path cache! (astranaut01 audit #22956)")
	}
	if CanFastPathCacheWithFreshness(vVerified, activeFreshness, "unknown-tenant") {
		t.Fatalf("SECURITY VIOLATION: unlisted tenant was permitted to reuse fast-path cache!")
	}
}

func TestVTP_SettlementZeroTrustReevaluation(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-settle-zt",
		Creator:  "creator-account",
		Bounty:   BountySpec{Currency: "GRN", Amount: 10},
	}

	// Verification was done by verifier-account
	verify := &TaskVerify{
		Protocol:           ProtocolVersion,
		Type:               "VERIFY",
		TaskID:             spec.TaskID,
		Verifier:           "verifier-account",
		Verdict:            "PASS",
		Basis:              "FACT_CONSISTENT",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
	}

	// Attack 1: Attempt to settle with payee == verifier (payee stealing validation bounty)
	_, err := SettleTask(spec, verify, "payer-account", "verifier-account", 15000)
	if err == nil {
		t.Fatalf("expected settlement failure when payee == verifier (usemarkbot audit #22300)")
	}

	// Attack 2: Attempt to settle with payee == creator
	_, err = SettleTask(spec, verify, "payer-account", "creator-account", 15000)
	if err == nil {
		t.Fatalf("expected settlement failure when payee == creator")
	}

	// Valid settlement: payer != payee != verifier != creator
	settle, err := SettleTask(spec, verify, "payer-account", "worker-account", 15000)
	if err != nil {
		t.Fatalf("unexpected settlement error on valid distinct accounts: %v", err)
	}
	if settle.Amount != 10 {
		t.Fatalf("expected amount 10, got %d", settle.Amount)
	}
}

func TestSettleTask_CrossTaskReplayRejected(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-legit-999",
		Creator:  "creator-account",
		Bounty:   BountySpec{Amount: 1000},
	}
	verifyAlien := &TaskVerify{
		Protocol:           ProtocolVersion,
		Type:               "VERIFY",
		TaskID:             "task-foreign-001",
		Verifier:           "verifier-account",
		Verdict:            "PASS",
		Basis:              "FACT_CONSISTENT",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
	}

	_, err := SettleTask(spec, verifyAlien, "payer-account", "worker-account", 15000)
	if err == nil {
		t.Fatalf("expected cross-task replay rejection when verify.TaskID != spec.TaskID")
	}
	if !strings.Contains(err.Error(), "does not match spec TaskID") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestVTP_CanonicalEncodingAndDelimiterInjection(t *testing.T) {
	// 1. Normalization invariance: permutation, case, duplicates
	caps1 := []string{"CAP_CHOWN", "CAP_DAC_OVERRIDE", "cap_chown"}
	caps2 := []string{"cap_dac_override", "CAP_CHOWN"}
	norm1 := NormalizeCapabilities(caps1)
	norm2 := NormalizeCapabilities(caps2)
	if len(norm1) != 2 || len(norm2) != 2 {
		t.Fatalf("expected 2 normalized capabilities, got %d and %d", len(norm1), len(norm2))
	}
	if norm1[0] != norm2[0] || norm1[1] != norm2[1] {
		t.Fatalf("expected identical normalized capabilities, got %v vs %v", norm1, norm2)
	}

	bytes1 := ComputeAttestationCanonicalBytes("task-1", "iss-1", "run-1", "policy-1", 1, "img-1", "inp-1", "k1", caps1, 100, 200)
	bytes2 := ComputeAttestationCanonicalBytes("task-1", "iss-1", "run-1", "policy-1", 1, "img-1", "inp-1", "k1", caps2, 100, 200)
	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("expected identical canonical bytes for permuted/duplicate caps, got differences:\n%s\nvs\n%s", string(bytes1), string(bytes2))
	}

	// 2. Delimiter collision resistance: length-delimited framing prevents field boundary bleeding
	// Attempt collision: task_id="t:key_id:k1", key_id="k2" vs task_id="t", key_id="k1:policy_digest:p"
	bA := ComputeAttestationCanonicalBytes("t:key_id:k1", "iss", "run", "p", 1, "img", "inp", "k2", nil, 100, 200)
	bB := ComputeAttestationCanonicalBytes("t", "iss", "run", "p", 1, "img", "inp", "k1:key_id:k2", nil, 100, 200)
	if bytes.Equal(bA, bB) {
		t.Fatalf("length-delimited encoding failed to prevent delimiter boundary bleeding!")
	}
}

func TestVTP_ED25519CryptographicAttestation(t *testing.T) {
	// Generate real ED25519 runner keypair
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate ed25519 keypair: %v", err)
	}

	keyID := "runner-enclave-key-2026-v1"
	runnerID := "runner-enclave-instance-1"
	issuer := "trusted-runner-enclave"

	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{issuer},
		ApprovedPolicyDigests: []string{"sha256:strict-gvisor-v1"},
		PublicKeys:            map[string]ed25519.PublicKey{keyID: pubKey},
		CurrentEpoch:          1,
		MinAcceptedEpoch:      1,
		CurrentTime:           1500,
	}

	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-ed25519-valid",
	}

	canonicalBytes := ComputeAttestationCanonicalBytes(
		spec.TaskID,
		issuer,
		runnerID,
		"sha256:strict-gvisor-v1",
		1,
		"sha256:rootfs-image-v1",
		"sha256:input-manifest-v1",
		keyID,
		[]string{"cap_chown"},
		1000,
		2000,
	)

	// Sign canonically with ED25519 private key
	sig := ed25519.Sign(privKey, canonicalBytes)
	sigHex := hex.EncodeToString(sig)

	receipt := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       issuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: "sha256:strict-gvisor-v1",
				PolicyEpoch:  1,
				RuntimeImage: "sha256:rootfs-image-v1",
				InputDigest:  "sha256:input-manifest-v1",
				AllowedCaps:  []string{"cap_chown"},
				IssuedAt:     1000,
				ExpiresAt:    2000,
				Signature:    sigHex,
			},
		},
	}

	status, reason := DeriveHermeticityEvaluation(spec, receipt, allowlist)
	if status != HermeticStatusVerifiedHermetic || reason != HermeticReasonVerified {
		t.Fatalf("expected VERIFIED_HERMETIC & REASON_VERIFIED for valid ED25519 attestation, got %s (%s)", status, reason)
	}

	verify := &TaskVerify{
		Protocol:           ProtocolVersion,
		Type:               "VERIFY",
		TaskID:             spec.TaskID,
		Verdict:            "PASS",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
		HermeticityStatus:  status,
		HermeticityReason:  reason,
		IsHermetic:         status == HermeticStatusVerifiedHermetic,
	}

	if !CanFastPathCache(verify) {
		t.Fatalf("expected CanFastPathCache=true for verified ED25519 attestation")
	}
}

func TestVTP_AstranautNegativeHashTamper(t *testing.T) {
	// Negative test specifically requested by @astranaut01 (#22461):
	// Attacker tampers with attestation (e.g. injects malicious runtime image)
	// and recomputes the SHA256 hash. Without the runner's private key,
	// ED25519 verification MUST fail and status MUST remain UNKNOWN.

	pubKey, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate ed25519 keypair: %v", err)
	}

	keyID := "runner-enclave-key-2026-v1"
	runnerID := "runner-enclave-instance-1"
	issuer := "trusted-runner-enclave"

	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{issuer},
		ApprovedPolicyDigests: []string{"sha256:strict-gvisor-v1"},
		PublicKeys:            map[string]ed25519.PublicKey{keyID: pubKey},
		CurrentEpoch:          1,
		MinAcceptedEpoch:      1,
		CurrentTime:           1500,
	}

	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-astranaut-tamper",
	}

	// Attacker tampers with image and recomputes SHA-256 hash as the "signature"
	tamperedImage := "sha256:malicious-compromised-image"
	tamperedCanonical := ComputeAttestationCanonicalBytes(
		spec.TaskID,
		issuer,
		runnerID,
		"sha256:strict-gvisor-v1",
		1,
		tamperedImage,
		"sha256:input-manifest-v1",
		keyID,
		[]string{"cap_chown"},
		1000,
		2000,
	)
	tamperedSHA := ComputeDigest(tamperedCanonical) // 32 bytes hex, not valid 64-byte ed25519 signature!

	receipt := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       issuer,
				RunnerID:     runnerID,
				KeyID:        keyID,
				PolicyDigest: "sha256:strict-gvisor-v1",
				PolicyEpoch:  1,
				RuntimeImage: tamperedImage,
				InputDigest:  "sha256:input-manifest-v1",
				AllowedCaps:  []string{"cap_chown"},
				IssuedAt:     1000,
				ExpiresAt:    2000,
				Signature:    tamperedSHA,
			},
		},
	}

	status, reason := DeriveHermeticityEvaluation(spec, receipt, allowlist)
	if status != HermeticStatusUnknown || reason != HermeticReasonSignatureInvalid {
		t.Fatalf("astranaut01 audit violation: recomputed SHA hash over tampered image must yield UNKNOWN & REASON_SIGNATURE_INVALID, got %s (%s)", status, reason)
	}
}

func TestVTP_AttestationLifecycleAndRevocation(t *testing.T) {
	// Audit tests for @second-thought (#22431):
	// Tests key revocation, epoch bumping, and expiration boundaries.
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	keyID := "runner-key-epoch-1"
	runnerID := "runner-node-lifecycle"
	issuer := "trusted-runner-enclave"

	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{issuer},
		ApprovedPolicyDigests: []string{"sha256:strict-gvisor-v1"},
		PublicKeys:            map[string]ed25519.PublicKey{keyID: pubKey},
		RevokedKeyIDs:         map[string]bool{},
		CurrentEpoch:          1,
		MinAcceptedEpoch:      1,
		CurrentTime:           1500,
	}

	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-lifecycle",
	}

	makeReceipt := func(epoch uint64, issuedAt, expiresAt int64) *TaskReceipt {
		b := ComputeAttestationCanonicalBytes(spec.TaskID, issuer, runnerID, "sha256:strict-gvisor-v1", epoch, "img-1", "inp-1", keyID, nil, issuedAt, expiresAt)
		sig := ed25519.Sign(privKey, b)
		return &TaskReceipt{
			Protocol: ProtocolVersion,
			TaskID:   spec.TaskID,
			Worker:   "worker-node",
			Execution: ExecutionReceipt{
				ExitCode: 0,
				Hermetic: true,
				Sandbox: &SandboxAttestation{
					Issuer:       issuer,
					RunnerID:     runnerID,
					KeyID:        keyID,
					PolicyDigest: "sha256:strict-gvisor-v1",
					PolicyEpoch:  epoch,
					RuntimeImage: "img-1",
					InputDigest:  "inp-1",
					IssuedAt:     issuedAt,
					ExpiresAt:    expiresAt,
					Signature:    hex.EncodeToString(sig),
				},
			},
		}
	}

	// Case 1: Valid active lifecycle
	recValid := makeReceipt(1, 1000, 2000)
	if s, r := DeriveHermeticityEvaluation(spec, recValid, allowlist); s != HermeticStatusVerifiedHermetic || r != HermeticReasonVerified {
		t.Fatalf("expected VERIFIED_HERMETIC & REASON_VERIFIED for active attestation, got %s (%s)", s, r)
	}

	// Case 2: Key Revocation
	allowlist.RevokedKeyIDs[keyID] = true
	if s, r := DeriveHermeticityEvaluation(spec, recValid, allowlist); s != HermeticStatusUnknown || r != HermeticReasonKeyRevoked {
		t.Fatalf("expected UNKNOWN & REASON_KEY_REVOKED for revoked key_id, got %s (%s)", s, r)
	}
	allowlist.RevokedKeyIDs[keyID] = false // un-revoke for next checks

	// Case 3: Expired attestation (now > expires_at)
	recExpired := makeReceipt(1, 500, 1200) // expires at 1200, current time is 1500
	if s, r := DeriveHermeticityEvaluation(spec, recExpired, allowlist); s != HermeticStatusUnknown || r != HermeticReasonExpired {
		t.Fatalf("expected UNKNOWN & REASON_EXPIRED for expired attestation, got %s (%s)", s, r)
	}

	// Case 4: Future attestation (now < issued_at)
	recFuture := makeReceipt(1, 1800, 2500) // issued at 1800, current time is 1500
	if s, r := DeriveHermeticityEvaluation(spec, recFuture, allowlist); s != HermeticStatusUnknown || r != HermeticReasonFutureIssuedAt {
		t.Fatalf("expected UNKNOWN & REASON_FUTURE_ISSUED_AT for future attestation, got %s (%s)", s, r)
	}

	// Case 5: Policy Epoch Bumping (min accepted epoch bumped to 2)
	allowlist.MinAcceptedEpoch = 2
	if s, r := DeriveHermeticityEvaluation(spec, recValid, allowlist); s != HermeticStatusUnknown || r != HermeticReasonEpochStale {
		t.Fatalf("expected UNKNOWN & REASON_EPOCH_STALE for outdated policy epoch, got %s (%s)", s, r)
	}

	// Case 6: Attestation with updated epoch 2 -> VERIFIED_HERMETIC
	recEpoch2 := makeReceipt(2, 1000, 2000)
	if s, r := DeriveHermeticityEvaluation(spec, recEpoch2, allowlist); s != HermeticStatusVerifiedHermetic || r != HermeticReasonVerified {
		t.Fatalf("expected VERIFIED_HERMETIC & REASON_VERIFIED for epoch 2 attestation, got %s (%s)", s, r)
	}
}

func TestVTP_RawVsLFProjection_Astranaut01(t *testing.T) {
	// Exact scenario from astranaut01 #23145:
	// Byte sequence 1: 41 0a ("A\n")
	// Byte sequence 2: 41 0d 0a ("A\r\n")
	bytesLF := []byte{0x41, 0x0a}
	bytesCRLF := []byte{0x41, 0x0d, 0x0a}

	rawLF := ComputeRawDigest(bytesLF)
	rawCRLF := ComputeRawDigest(bytesCRLF)
	projLF := ComputeDigest(bytesLF)
	projCRLF := ComputeDigest(bytesCRLF)

	// 1. Under raw byte evaluation, they MUST diverge (raw_equal == false)
	if rawLF == rawCRLF {
		t.Fatalf("raw digests unexpectedly matched for 41 0a vs 41 0d 0a: %s", rawLF)
	}

	// 2. Under LF projection evaluation, they MUST agree (LF_projection_equal == true)
	if projLF != projCRLF {
		t.Fatalf("LF projection digests unexpectedly diverged for 41 0a vs 41 0d 0a: %s vs %s", projLF, projCRLF)
	}

	// 3. Test CompareDigestProjections utility
	cmp := CompareDigestProjections(bytesLF, bytesCRLF)
	if cmp.RawEqual {
		t.Errorf("CompareDigestProjections reported raw_equal == true for LF vs CRLF bytes")
	}
	if !cmp.LFProjectionEqual {
		t.Errorf("CompareDigestProjections reported LF_projection_equal == false for LF vs CRLF bytes")
	}
}

func TestVTP_ReceiptHash_ZcodeAvikhMatch(t *testing.T) {
	// Exact payload and test vector from zcode-avikh #23166
	receipt := &TaskReceipt{
		Protocol: "VTP/1.0",
		Type:     "RECEIPT",
		TaskID:   "WP-0007-GENOME-R7",
		Worker:   "antigravity-wanderer",
		Execution: ExecutionReceipt{
			StdoutSHA256:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			DiffHunksSHA256:    "4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b",
			ExecutedAssertions: 20,
			ExitCode:           0,
		},
		Artifacts:      []string{"evidence/r7_output.json", "tests/verify_invariants.go"},
		IdempotencyKey: "c8a6f40b-0447-4cfc-b8e7-142589021760",
	}

	hash, err := ReceiptHash(receipt)
	if err != nil {
		t.Fatalf("ReceiptHash failed: %v", err)
	}
	want := "e536ad8cf230799fa9d8d754f049c1d007d4c770f101452867d9ec5932c91310"
	if hash != want {
		t.Fatalf("ReceiptHash mismatch: got %s, want %s", hash, want)
	}

	canonBytes, err := CanonicalJSON(receipt)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	wantCanon := `{"artifacts":["evidence/r7_output.json","tests/verify_invariants.go"],"execution":{"diff_hunks_sha256":"4a5e1e4baab89f3a32518a88c31bc87f618f76673e2cc77ab2127b7afdeda33b","executed_assertions":20,"exit_code":0,"stdout_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},"idempotency_key":"c8a6f40b-0447-4cfc-b8e7-142589021760","protocol":"VTP/1.0","task_id":"WP-0007-GENOME-R7","type":"RECEIPT","worker":"antigravity-wanderer"}`
	if string(canonBytes) != wantCanon {
		t.Fatalf("CanonicalJSON mismatch:\ngot:  %s\nwant: %s", string(canonBytes), wantCanon)
	}
}

func TestCanonicalJSON_RFC8785_UTF16SortOrder(t *testing.T) {
	// In UTF-8: "\uE000" is 3 bytes (0xEE...), "\U00010000" is 4 bytes (0xF0...)
	// In standard UTF-8 byte comparison: "\uE000" < "\U00010000"
	// In RFC 8785 UTF-16 code units: "\U00010000" is encoded as 0xD800 0xDC00,
	// while "\uE000" is 0xE000. Since 0xD800 < 0xE000, "\U00010000" MUST sort first!
	m := map[string]int{
		"\uE000":     1,
		"\U00010000": 2,
	}
	b, err := CanonicalJSON(m)
	if err != nil {
		t.Fatalf("CanonicalJSON failed: %v", err)
	}
	want := `{"𐀀":2,"":1}`
	if string(b) != want {
		t.Fatalf("RFC 8785 UTF-16 sort order mismatch: got %s, want %s", string(b), want)
	}
}

func TestVerifyReceipt_IdentifiableAssertions(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-assert-ident",
		Oracle: OracleSpec{
			Type:       "DETERMINISTIC",
			Assertions: []string{"check_auth", "check_db", "check_audit"},
		},
	}

	// Case 1: Partial / failing assertion in receipt
	receiptFail := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-1",
		Execution: ExecutionReceipt{
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			ExitCode:     0,
			AssertionResults: []AssertionResult{
				{ID: "check_auth", Passed: true},
				{ID: "check_db", Passed: false, Evidence: "connection refused"},
				{ID: "check_audit", Passed: true},
			},
		},
	}
	v, err := VerifyReceipt(spec, receiptFail, []byte(""), []byte(""), 0, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "FAIL" || v.Basis != "UNSATISFIED_ASSERTIONS" {
		t.Fatalf("expected FAIL with UNSATISFIED_ASSERTIONS, got %s (%s)", v.Verdict, v.Basis)
	}

	// Case 2: All assertions passed
	receiptPass := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-1",
		Execution: ExecutionReceipt{
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			ExitCode:     0,
			AssertionResults: []AssertionResult{
				{ID: "check_auth", Passed: true},
				{ID: "check_db", Passed: true},
				{ID: "check_audit", Passed: true},
			},
		},
	}
	vPass, err := VerifyReceipt(spec, receiptPass, []byte(""), []byte(""), 0, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if vPass.Verdict != "PASS" {
		t.Fatalf("expected PASS, got %s (%s)", vPass.Verdict, vPass.Basis)
	}

	// Case 3: Adversary attempts assertion count spoofing (ExecutedAssertions=100, AssertionResults=nil)
	receiptSpoof := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-1",
		Execution: ExecutionReceipt{
			StdoutSHA256:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			ExitCode:           0,
			ExecutedAssertions: 100, // Spoofed count
			AssertionResults:   nil, // Missing identifiable results
		},
	}
	vSpoof, err := VerifyReceipt(spec, receiptSpoof, []byte(""), []byte(""), 0, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if vSpoof.Verdict != "FAIL" || vSpoof.Basis != "UNSATISFIED_ASSERTIONS" {
		t.Fatalf("expected FAIL with UNSATISFIED_ASSERTIONS for spoofed count without results, got %s (%s)", vSpoof.Verdict, vSpoof.Basis)
	}

	// Case 4: Duplicate assertion result
	receiptDup := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-1",
		Execution: ExecutionReceipt{
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			ExitCode:     0,
			AssertionResults: []AssertionResult{
				{ID: "check_auth", Passed: true},
				{ID: "check_auth", Passed: true},
				{ID: "check_db", Passed: true},
				{ID: "check_audit", Passed: true},
			},
		},
	}
	vDup, err := VerifyReceipt(spec, receiptDup, []byte(""), []byte(""), 0, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if vDup.Verdict != "FAIL" || vDup.Basis != "DUPLICATE_ASSERTION_RESULT" {
		t.Fatalf("expected FAIL with DUPLICATE_ASSERTION_RESULT, got %s (%s)", vDup.Verdict, vDup.Basis)
	}
}

func TestVerifyReceipt_SpecCommitmentMismatch(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-commit-check",
		Title:    "Legitimate Task",
		Oracle: OracleSpec{
			Type: "DETERMINISTIC",
		},
	}
	receipt := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-1",
		Commitments: &CommitmentSpec{
			SpecSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		},
		Execution: ExecutionReceipt{
			StdoutSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			ExitCode:     0,
		},
	}
	v, err := VerifyReceipt(spec, receipt, []byte(""), []byte(""), 0, "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if v.Verdict != "FAIL" || v.Basis != "SPEC_COMMITMENT_MISMATCH" {
		t.Fatalf("expected FAIL with SPEC_COMMITMENT_MISMATCH, got %s (%s)", v.Verdict, v.Basis)
	}
}

func TestSettleTask_HermeticGateEnforced(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-herm-gate",
		Creator:  "creator-alice",
		Bounty:   BountySpec{Amount: 100},
		Oracle: OracleSpec{
			Hermetic: true,
		},
	}

	// Verification with non-hermetic execution status
	verifyNonHerm := &TaskVerify{
		Protocol:           ProtocolVersion,
		TaskID:             spec.TaskID,
		Verifier:           "verifier-bob",
		Verdict:            "PASS",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
		HermeticityStatus:  HermeticStatusUnknown,
		HermeticityReason:  HermeticReasonMissingSandbox,
		IsHermetic:         false,
	}
	_, err := SettleTask(spec, verifyNonHerm, "creator-alice", "worker-charlie", 10)
	if err == nil {
		t.Fatal("expected settlement failure for unverified hermeticity when spec.Oracle.Hermetic is true")
	}
	if !strings.Contains(err.Error(), "spec requires hermetic execution") {
		t.Fatalf("expected 'spec requires hermetic execution' error, got: %v", err)
	}

	// Verification with VERIFIED_HERMETIC status
	verifyHerm := &TaskVerify{
		Protocol:           ProtocolVersion,
		TaskID:             spec.TaskID,
		Verifier:           "verifier-bob",
		Verdict:            "PASS",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
		HermeticityStatus:  HermeticStatusVerifiedHermetic,
		HermeticityReason:  HermeticReasonVerified,
		IsHermetic:         true,
	}
	settle, err := SettleTask(spec, verifyHerm, "creator-alice", "worker-charlie", 10)
	if err != nil {
		t.Fatalf("unexpected settlement error for verified hermetic task: %v", err)
	}
	if settle.Amount != 100 {
		t.Fatalf("expected bounty amount 100, got %d", settle.Amount)
	}
}

func TestTaskVerify_SignatureAuthentication(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-sig-auth",
		Creator:  "creator-node",
		Bounty:   BountySpec{Amount: 50},
	}
	verify := &TaskVerify{
		Protocol:           ProtocolVersion,
		TaskID:             spec.TaskID,
		Verifier:           "verifier-node",
		Verdict:            "PASS",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
	}

	// Sign TaskVerify
	if err := SignTaskVerify(verify, privKey, "verifier-key-1"); err != nil {
		t.Fatal(err)
	}
	if verify.Signature == "" || verify.VerifierKeyID != "verifier-key-1" {
		t.Fatal("signature not set")
	}

	// Verify signature helper directly
	valid, err := VerifyTaskVerifySignature(verify, pubKey)
	if err != nil || !valid {
		t.Fatalf("VerifyTaskVerifySignature failed: %v", err)
	}

	// Settlement with allowlist containing the public key
	allowlist := &HermeticAllowlist{
		PublicKeys: map[string]ed25519.PublicKey{
			"verifier-key-1": pubKey,
		},
	}
	settle, err := SettleTaskWithAllowlist(spec, verify, "creator-node", "worker-node", 1, allowlist)
	if err != nil {
		t.Fatalf("SettleTaskWithAllowlist failed: %v", err)
	}
	if settle.Amount != 50 {
		t.Errorf("amount mismatch: %d", settle.Amount)
	}

	// Reject if key is unregistered or signature tampered
	verifyTampered := *verify
	verifyTampered.Signature = hex.EncodeToString(make([]byte, 64))
	_, err = SettleTaskWithAllowlist(spec, &verifyTampered, "creator-node", "worker-node", 1, allowlist)
	if err == nil {
		t.Fatal("expected settlement failure for tampered signature")
	}
}





