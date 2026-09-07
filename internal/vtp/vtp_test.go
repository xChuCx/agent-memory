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

	// 4. Verification (Disjoint Seat Stranger)
	verify, err := VerifyReceipt(spec, receipt, mockStdout, mockDiff, 0, "orca-agent", true)
	if err != nil {
		t.Fatalf("unexpected verification error: %v", err)
	}
	if verify.Verdict != "PASS" {
		t.Fatalf("expected PASS, got %s (basis: %s)", verify.Verdict, verify.Basis)
	}
	if !verify.IsDisjointSeat {
		t.Fatalf("expected disjoint seat flag")
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
		Bounty:   BountySpec{Currency: "GRN", Amount: 1},
	}
	receipt := &TaskReceipt{
		Protocol: ProtocolVersion,
		TaskID:   spec.TaskID,
		Execution: ExecutionReceipt{
			StdoutSHA256: "expected_hash",
			ExitCode:     0,
		},
		IdempotencyKey: "rec-001",
	}

	// Negative control 1: Non-zero exit code
	v1, err := VerifyReceipt(spec, receipt, []byte("fail"), []byte("diff"), 1, "verifier", true)
	if err != nil || v1.Verdict != "FAIL" || v1.Basis != "NON_ZERO_EXIT" {
		t.Fatalf("expected NON_ZERO_EXIT failure, got %+v", v1)
	}

	// Negative control 2: Digest mismatch
	v2, err := VerifyReceipt(spec, receipt, []byte("tampered output"), []byte("diff"), 0, "verifier", true)
	if err != nil || v2.Verdict != "FAIL" || v2.Basis != "STDOUT_DIGEST_MISMATCH" {
		t.Fatalf("expected STDOUT_DIGEST_MISMATCH failure, got %+v", v2)
	}

	// Negative control 3: Non-disjoint settlement rejection (Clause B)
	v3 := &TaskVerify{
		Verdict:        "PASS",
		IsDisjointSeat: false, // Self-verification violation!
	}
	_, err = SettleTask(spec, v3, "payer", "payee", 14450)
	if err == nil {
		t.Fatalf("expected Clause B settlement failure when is_disjoint_seat is false")
	}
}

func TestVTP_PartialSettlement(t *testing.T) {
	spec := &TaskSpec{
		Protocol: ProtocolVersion,
		TaskID:   "task-vtp-partial",
		Bounty:   BountySpec{Currency: "GRN", Amount: 10},
	}
	verify := &TaskVerify{
		Protocol:       ProtocolVersion,
		Type:           "VERIFY",
		TaskID:         spec.TaskID,
		Verdict:        "PARTIAL",
		Basis:          "CONTAMINATION_GAME_THEORY",
		EvidenceSHA256: "evidence_partial_hash",
		IsDisjointSeat: true,
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
		t.Fatalf("unexpected error on min partial settlement: %v", err)
	}
	if settleMin.Amount != 1 {
		t.Fatalf("expected minimum 1 GRN payout, got %d", settleMin.Amount)
	}
}

func TestVTP_DeriveDisjointSeat(t *testing.T) {
	cases := []struct {
		name      string
		worker    string
		verifier  string
		creator   string
		expected  bool
	}{
		{"disjoint valid", "worker-01", "verifier-02", "creator-00", true},
		{"self-verification forbidden", "worker-01", "worker-01", "creator-00", false},
		{"creator cannot self-verify", "worker-01", "creator-00", "creator-00", false},
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

