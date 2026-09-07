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

func mustWriteTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write test file %s: %v", path, err)
	}
}

func TestCLIVTP_Digest(t *testing.T) {
	tmpDir := t.TempDir()
	sampleFile := filepath.Join(tmpDir, "sample.txt")
	mustWriteTestFile(t, sampleFile, []byte("hello world\r\n"))

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
	specBytes, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	specFile := filepath.Join(tmpDir, "spec.json")
	mustWriteTestFile(t, specFile, specBytes)

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
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	receiptFile := filepath.Join(tmpDir, "receipt.json")
	mustWriteTestFile(t, receiptFile, receiptBytes)

	stdoutFile := filepath.Join(tmpDir, "stdout.txt")
	mustWriteTestFile(t, stdoutFile, stdoutData)

	diffFile := filepath.Join(tmpDir, "diff.patch")
	mustWriteTestFile(t, diffFile, diffData)

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
	if !verifyArtifact.DistinctAccountIDs {
		t.Fatalf("expected DistinctAccountIDs to be true")
	}

	verifyFile := filepath.Join(tmpDir, "verify.json")
	mustWriteTestFile(t, verifyFile, verifyOut.Bytes())

	// 2. Settle
	rootSettle := NewRootCmd()
	var settleOut bytes.Buffer
	rootSettle.SetOut(&settleOut)
	rootSettle.SetArgs([]string{
		"vtp", "settle",
		"--spec", specFile,
		"--verify", verifyFile,
		"--payer", "@payer",
		"--payee", "@worker",
		"--seq", "14450",
		"--json",
	})

	if err := rootSettle.Execute(); err != nil {
		t.Fatalf("settle command failed: %v", err)
	}

	var settleArtifact vtp.TaskSettle
	if err := json.Unmarshal(settleOut.Bytes(), &settleArtifact); err != nil {
		t.Fatalf("unmarshal settle artifact: %v", err)
	}
	if settleArtifact.Amount != 1 || settleArtifact.SettlementMethod != "GRN_TRANSFER" {
		t.Fatalf("invalid settlement artifact: %+v", settleArtifact)
	}
}

func TestCLIVTP_ClauseBFailure(t *testing.T) {
	tmpDir := t.TempDir()

	spec := vtp.TaskSpec{
		Protocol: vtp.ProtocolVersion,
		TaskID:   "task-clause-b",
		Creator:  "@creator",
		Bounty: vtp.BountySpec{
			Currency: "GRN",
			Amount:   1,
		},
	}
	specBytes, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	specFile := filepath.Join(tmpDir, "spec.json")
	mustWriteTestFile(t, specFile, specBytes)

	receipt := vtp.TaskReceipt{
		Protocol:       vtp.ProtocolVersion,
		Type:           "RECEIPT",
		TaskID:         "task-clause-b",
		Worker:         "@worker",
		IdempotencyKey: "idem-b",
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		t.Fatalf("marshal receipt: %v", err)
	}
	receiptFile := filepath.Join(tmpDir, "receipt.json")
	mustWriteTestFile(t, receiptFile, receiptBytes)

	// Verify WITH self-verification (verifier == worker)
	rootVerify := NewRootCmd()
	var verifyOut bytes.Buffer
	rootVerify.SetOut(&verifyOut)
	rootVerify.SetArgs([]string{
		"vtp", "verify",
		"--spec", specFile,
		"--receipt", receiptFile,
		"--verifier", "@worker",
		"--json",
	})

	if err := rootVerify.Execute(); err != nil {
		t.Fatalf("verify command failed: %v", err)
	}

	verifyFile := filepath.Join(tmpDir, "verify_same_seat.json")
	mustWriteTestFile(t, verifyFile, verifyOut.Bytes())

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

	err = rootSettle.Execute()
	if err == nil {
		t.Fatalf("expected settle error for non-disjoint seat, got nil")
	}
	if !strings.Contains(err.Error(), "Clause B") {
		t.Fatalf("expected Clause B error, got: %v", err)
	}
}
