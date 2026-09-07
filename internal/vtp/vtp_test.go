package vtp

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
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
			ExitCode:           0,
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
	settleMin, err := SettleTask(specMin, verify, "payer-node", "worker-node", 15001)
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
		TaskID:   "task-vtp-hermetic-001",
		Title:    "Hermetic execution test",
		Bounty:   BountySpec{Currency: "GRN", Amount: 5},
		Oracle:   OracleSpec{Type: "execution@1", Hermetic: true},
	}

	trustedIssuer := "runner-enclave-v1"
	approvedPolicy := "sha256:hermetic-sandbox-policy-v1"
	validImage := "docker.io/library/alpine@sha256:e1c0d"
	validInput := "sha256:input-files-v1"
	allowedCaps := []string{"CAP_SYS_RESOURCE"}

	validSig := ComputeAttestationDigest(spec.TaskID, approvedPolicy, validImage, validInput, allowedCaps)

	// Case (c): Valid attestation + allowlisted policy + trusted issuer over exact bound tuple -> VERIFIED_HERMETIC
	recVerified := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Worker:   "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				PolicyDigest: approvedPolicy,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				Signature:    validSig,
			},
		},
	}
	vVerified, err := VerifyReceipt(spec, recVerified, []byte(""), []byte(""), 0, "verifier-node")
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vVerified.HermeticityStatus != HermeticStatusVerifiedHermetic || !vVerified.IsHermetic {
		t.Fatalf("expected VERIFIED_HERMETIC, got %s", vVerified.HermeticityStatus)
	}
	if !CanFastPathCache(vVerified) {
		t.Fatalf("expected CanFastPathCache=true for VERIFIED_HERMETIC PASS")
	}

	// Case (a): Hermetic=true + arbitrary unapproved policy digest -> UNKNOWN (second-thought audit #22398)
	bogusSig := ComputeAttestationDigest(spec.TaskID, "sha256:arbitrary-bogus-policy", validImage, validInput, allowedCaps)
	recBogusPolicy := &TaskReceipt{
		Protocol:  ProtocolVersion,
		TaskID:    spec.TaskID,
		Worker:    "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				PolicyDigest: "sha256:arbitrary-bogus-policy", // Not on verifier allowlist
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				Signature:    bogusSig,
			},
		},
	}
	vBogus, err := VerifyReceipt(spec, recBogusPolicy, []byte(""), []byte(""), 0, "verifier-node")
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vBogus.HermeticityStatus != HermeticStatusUnknown || vBogus.IsHermetic {
		t.Fatalf("expected UNKNOWN for unapproved policy, got %s", vBogus.HermeticityStatus)
	}
	if CanFastPathCache(vBogus) {
		t.Fatalf("expected CanFastPathCache=FALSE for unapproved policy")
	}

	// Case (b): Valid signature but for mismatched image/input -> UNKNOWN
	tamperedSig := ComputeAttestationDigest("other-task-id", approvedPolicy, validImage, validInput, allowedCaps)
	recMismatched := &TaskReceipt{
		Protocol:  ProtocolVersion,
		TaskID:    spec.TaskID,
		Worker:    "worker-node",
		Execution: ExecutionReceipt{
			ExitCode: 0,
			Hermetic: true,
			Sandbox: &SandboxAttestation{
				Issuer:       trustedIssuer,
				PolicyDigest: approvedPolicy,
				RuntimeImage: validImage,
				InputDigest:  validInput,
				AllowedCaps:  allowedCaps,
				Signature:    tamperedSig, // Bound to other-task-id!
			},
		},
	}
	vMismatched, err := VerifyReceipt(spec, recMismatched, []byte(""), []byte(""), 0, "verifier-node")
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vMismatched.HermeticityStatus != HermeticStatusUnknown || vMismatched.IsHermetic {
		t.Fatalf("expected UNKNOWN for mismatched attestation signature, got %s", vMismatched.HermeticityStatus)
	}
	if CanFastPathCache(vMismatched) {
		t.Fatalf("expected CanFastPathCache=FALSE for mismatched attestation signature")
	}

	// Case (d): Declared Non-hermetic execution receipt -> DECLARED_NON_HERMETIC
	recNonHermetic := &TaskReceipt{
		Protocol:  ProtocolVersion,
		TaskID:    spec.TaskID,
		Worker:    "worker-node",
		Execution: ExecutionReceipt{ExitCode: 0, Hermetic: false},
	}
	vNonHermetic, err := VerifyReceipt(spec, recNonHermetic, []byte(""), []byte(""), 0, "verifier-node")
	if err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
	if vNonHermetic.HermeticityStatus != HermeticStatusDeclaredNonHermetic || vNonHermetic.IsHermetic {
		t.Fatalf("expected DECLARED_NON_HERMETIC, got %s", vNonHermetic.HermeticityStatus)
	}
	if CanFastPathCache(vNonHermetic) {
		t.Fatalf("expected CanFastPathCache=FALSE for DECLARED_NON_HERMETIC")
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

	bytes1 := ComputeAttestationCanonicalBytes("task-1", "policy-1", 1, "img-1", "inp-1", "k1", caps1, 100, 200)
	bytes2 := ComputeAttestationCanonicalBytes("task-1", "policy-1", 1, "img-1", "inp-1", "k1", caps2, 100, 200)
	if !bytes.Equal(bytes1, bytes2) {
		t.Fatalf("expected identical canonical bytes for permuted/duplicate caps, got differences:\n%s\nvs\n%s", string(bytes1), string(bytes2))
	}

	// 2. Delimiter collision resistance: length-delimited framing prevents field boundary bleeding
	// Attempt collision: task_id="t:key_id:k1", key_id="k2" vs task_id="t", key_id="k1:policy_digest:p"
	bA := ComputeAttestationCanonicalBytes("t:key_id:k1", "p", 1, "img", "inp", "k2", nil, 0, 0)
	bB := ComputeAttestationCanonicalBytes("t", "p", 1, "img", "inp", "k1:key_id:k2", nil, 0, 0)
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
	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{"trusted-runner-enclave"},
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
				Issuer:       "trusted-runner-enclave",
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

	status := DeriveHermeticityStatus(spec, receipt, allowlist)
	if status != HermeticStatusVerifiedHermetic {
		t.Fatalf("expected VERIFIED_HERMETIC for valid ED25519 attestation, got %s", status)
	}

	verify := &TaskVerify{
		Protocol:           ProtocolVersion,
		Type:               "VERIFY",
		TaskID:             spec.TaskID,
		Verdict:            "PASS",
		DistinctAccountIDs: true,
		IsDisjointSeat:     true,
		HermeticityStatus:  status,
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
	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{"trusted-runner-enclave"},
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
				Issuer:       "trusted-runner-enclave",
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

	status := DeriveHermeticityStatus(spec, receipt, allowlist)
	if status != HermeticStatusUnknown {
		t.Fatalf("astranaut01 audit violation: recomputed SHA hash over tampered image must yield UNKNOWN, got %s", status)
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
	allowlist := &HermeticAllowlist{
		TrustedIssuers:        []string{"trusted-runner-enclave"},
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
		b := ComputeAttestationCanonicalBytes(spec.TaskID, "sha256:strict-gvisor-v1", epoch, "img-1", "inp-1", keyID, nil, issuedAt, expiresAt)
		sig := ed25519.Sign(privKey, b)
		return &TaskReceipt{
			Protocol: ProtocolVersion,
			TaskID:   spec.TaskID,
			Worker:   "worker-node",
			Execution: ExecutionReceipt{
				ExitCode: 0,
				Hermetic: true,
				Sandbox: &SandboxAttestation{
					Issuer:       "trusted-runner-enclave",
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
	if s := DeriveHermeticityStatus(spec, recValid, allowlist); s != HermeticStatusVerifiedHermetic {
		t.Fatalf("expected VERIFIED_HERMETIC for active attestation, got %s", s)
	}

	// Case 2: Key Revocation
	allowlist.RevokedKeyIDs[keyID] = true
	if s := DeriveHermeticityStatus(spec, recValid, allowlist); s != HermeticStatusUnknown {
		t.Fatalf("expected UNKNOWN for revoked key_id, got %s", s)
	}
	allowlist.RevokedKeyIDs[keyID] = false // un-revoke for next checks

	// Case 3: Expired attestation (now > expires_at)
	recExpired := makeReceipt(1, 500, 1200) // expires at 1200, current time is 1500
	if s := DeriveHermeticityStatus(spec, recExpired, allowlist); s != HermeticStatusUnknown {
		t.Fatalf("expected UNKNOWN for expired attestation, got %s", s)
	}

	// Case 4: Future attestation (now < issued_at)
	recFuture := makeReceipt(1, 1800, 2500) // issued at 1800, current time is 1500
	if s := DeriveHermeticityStatus(spec, recFuture, allowlist); s != HermeticStatusUnknown {
		t.Fatalf("expected UNKNOWN for future attestation, got %s", s)
	}

	// Case 5: Policy Epoch Bumping (min accepted epoch bumped to 2)
	allowlist.MinAcceptedEpoch = 2
	if s := DeriveHermeticityStatus(spec, recValid, allowlist); s != HermeticStatusUnknown {
		t.Fatalf("expected UNKNOWN for outdated policy epoch (attestation epoch=1, min=2), got %s", s)
	}

	// Case 6: Attestation with updated epoch 2 -> VERIFIED_HERMETIC
	recEpoch2 := makeReceipt(2, 1000, 2000)
	if s := DeriveHermeticityStatus(spec, recEpoch2, allowlist); s != HermeticStatusVerifiedHermetic {
		t.Fatalf("expected VERIFIED_HERMETIC for epoch 2 attestation, got %s", s)
	}
}



