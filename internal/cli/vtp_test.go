package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/vtp"
)

func TestCLIVTP_Digest(t *testing.T) {
	tmpDir := t.TempDir()
	sampleFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(sampleFile, []byte("hello world\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"vtp", "digest", sampleFile, "--json"})

	if err := root.Execute(); err != nil {
		t.Fatalf("vtp digest failed: %v", err)
	}

	var res map[string]string
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal json output: %v", err)
	}

	expected := vtp.ComputeDigest([]byte("hello world\n"))
	if res["sha256"] != expected {
		t.Fatalf("digest mismatch: expected %s, got %s", expected, res["sha256"])
	}
}

func TestCLIVTP_VerifyAndSettle(t *testing.T) {
	tmpDir := t.TempDir()

	spec := vtp.TaskSpec{
		Protocol: vtp.ProtocolVersion,
		TaskID:   "task-cli-001",
		Title:    "CLI VTP integration test",
		Bounty:   vtp.BountySpec{Currency: "GRN", Amount: 1},
		Oracle:   vtp.OracleSpec{Type: "execution@1"},
	}
	specBytes, _ := json.Marshal(spec)
	specFile := filepath.Join(tmpDir, "spec.json")
	os.WriteFile(specFile, specBytes, 0o644)

	stdoutData := []byte("PASS: all tests ok\n")
	diffData := []byte("diff --git a/test b/test\n")

	receipt := vtp.TaskReceipt{
		Protocol: vtp.ProtocolVersion,
		Type:     "RECEIPT",
		TaskID:   "task-cli-001",
		Worker:   "@test-worker",
		Execution: vtp.ExecutionReceipt{
			StdoutSHA256:    vtp.ComputeDigest(stdoutData),
			DiffHunksSHA256: vtp.ComputeDigest(diffData),
			ExitCode:        0,
		},
		IdempotencyKey: "test-idem-001",
	}
	receiptBytes, _ := json.Marshal(receipt)
	receiptFile := filepath.Join(tmpDir, "receipt.json")
	os.WriteFile(receiptFile, receiptBytes, 0o644)

	stdoutFile := filepath.Join(tmpDir, "stdout.txt")
	os.WriteFile(stdoutFile, stdoutData, 0o644)

	diffFile := filepath.Join(tmpDir, "diff.patch")
	os.WriteFile(diffFile, diffData, 0o644)

	// 1. Verify with disjoint=true
	rootVerify := NewRootCmd()
	var verifyOut bytes.Buffer
	rootVerify.SetOut(&verifyOut)
	rootVerify.SetArgs([]string{
		"vtp", "verify",
		"--spec", specFile,
		"--receipt", receiptFile,
		"--stdout", stdoutFile,
		"--diff", diffFile,
		"--verifier", "@independent-verifier",
		"--disjoint",
		"--json",
	})

	if err := rootVerify.Execute(); err != nil {
		t.Fatalf("vtp verify command failed: %v", err)
	}

	var verifyArtifact vtp.TaskVerify
	if err := json.Unmarshal(verifyOut.Bytes(), &verifyArtifact); err != nil {
		t.Fatalf("unmarshal verify artifact: %v", err)
	}
	if verifyArtifact.Verdict != "PASS" {
		t.Fatalf("expected PASS, got %s", verifyArtifact.Verdict)
	}
	if !verifyArtifact.IsDisjointSeat {
		t.Fatalf("expected IsDisjointSeat to be true")
	}

	verifyFile := filepath.Join(tmpDir, "verify.json")
	os.WriteFile(verifyFile, verifyOut.Bytes(), 0o644)

	// 2. Settle
	rootSettle := NewRootCmd()
	var settleOut bytes.Buffer
	rootSettle.SetOut(&settleOut)
	rootSettle.SetArgs([]string{
		"vtp", "settle",
		"--spec", specFile,
		"--verify", verifyFile,
		"--payer", "@payer-agent",
		"--payee", "@test-worker",
		"--seq", "14550",
		"--json",
	})

	if err := rootSettle.Execute(); err != nil {
		t.Fatalf("vtp settle command failed: %v", err)
	}

	var settleArtifact vtp.TaskSettle
	if err := json.Unmarshal(settleOut.Bytes(), &settleArtifact); err != nil {
		t.Fatalf("unmarshal settle artifact: %v", err)
	}
	if settleArtifact.Amount != 1 || settleArtifact.Payee != "@test-worker" {
		t.Fatalf("unexpected settlement payload: %+v", settleArtifact)
	}
}

func TestCLIVTP_ClauseBFailure(t *testing.T) {
	tmpDir := t.TempDir()

	spec := vtp.TaskSpec{
		Protocol: vtp.ProtocolVersion,
		TaskID:   "task-clause-b",
		Bounty:   vtp.BountySpec{Currency: "GRN", Amount: 1},
		Oracle:   vtp.OracleSpec{Type: "execution@1"},
	}
	specBytes, _ := json.Marshal(spec)
	specFile := filepath.Join(tmpDir, "spec.json")
	os.WriteFile(specFile, specBytes, 0o644)

	receipt := vtp.TaskReceipt{
		Protocol:       vtp.ProtocolVersion,
		Type:           "RECEIPT",
		TaskID:         "task-clause-b",
		Worker:         "@worker",
		IdempotencyKey: "idem-b",
	}
	receiptBytes, _ := json.Marshal(receipt)
	receiptFile := filepath.Join(tmpDir, "receipt.json")
	os.WriteFile(receiptFile, receiptBytes, 0o644)

	// Verify WITHOUT --disjoint
	rootVerify := NewRootCmd()
	var verifyOut bytes.Buffer
	rootVerify.SetOut(&verifyOut)
	rootVerify.SetArgs([]string{
		"vtp", "verify",
		"--spec", specFile,
		"--receipt", receiptFile,
		"--verifier", "@same-seat-agent",
		"--json",
	})

	if err := rootVerify.Execute(); err != nil {
		t.Fatalf("verify command failed: %v", err)
	}

	verifyFile := filepath.Join(tmpDir, "verify_same_seat.json")
	os.WriteFile(verifyFile, verifyOut.Bytes(), 0o644)

	// Attempt settle -> must fail due to Clause B
	rootSettle := NewRootCmd()
	var settleOut bytes.Buffer
	rootSettle.SetOut(&settleOut)
	rootSettle.SetArgs([]string{
		"vtp", "settle",
		"--spec", specFile,
		"--verify", verifyFile,
		"--payer", "@payer",
		"--payee", "@worker",
		"--seq", "14551",
	})

	err := rootSettle.Execute()
	if err == nil {
		t.Fatalf("expected settle error for non-disjoint seat, got nil")
	}
	if !strings.Contains(err.Error(), "Clause B") {
		t.Fatalf("expected Clause B error, got: %v", err)
	}
}
