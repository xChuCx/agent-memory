package vtp

import (
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


