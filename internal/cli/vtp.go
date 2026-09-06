package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/vtp"
)

// NewVTPCmd returns the `agent-memory vtp` command group.
func NewVTPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vtp",
		Short: "Verifiable Task Protocol (VTP/1.0) operations",
		Long: `Execute, verify, digest, and settle tasks under the Verifiable Task Protocol (VTP/1.0).

VTP-1 provides deterministic, machine-verifiable task lifecycle for autonomous AI agents:
  Phase 1: TASK-SPEC (requirements, oracle assertions, bounty)
  Phase 2: TASK-CLAIM (worker claim with TTL and idempotency key)
  Phase 3: TASK-RECEIPT (execution proof with normalized stdout/diff digests)
  Phase 4: TASK-VERIFY (independent dual-oracle assertion and Clause B disjoint check)
  Phase 5: TASK-SETTLE (economic payout or GRN ledger mint)`,
	}

	cmd.AddCommand(newVTPDigestCmd())
	cmd.AddCommand(newVTPVerifyCmd())
	cmd.AddCommand(newVTPSettleCmd())

	return cmd
}

func newVTPDigestCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "digest <file>",
		Short: "Compute canonical VTP/SAR-002 normalized SHA-256 digest of a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}
			digest := vtp.ComputeDigest(data)
			if asJSON {
				res := map[string]string{
					"path":   filePath,
					"sha256": digest,
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", digest, filePath)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON output")
	return cmd
}

func newVTPVerifyCmd() *cobra.Command {
	var (
		specPath       string
		receiptPath    string
		stdoutPath     string
		diffPath       string
		exitCode       int
		verifierHandle string
		isDisjoint     bool
		asJSON         bool
	)
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify a TaskReceipt against actual execution output and TaskSpec",
		RunE: func(cmd *cobra.Command, args []string) error {
			if receiptPath == "" {
				return fmt.Errorf("--receipt flag is required")
			}
			receiptBytes, err := os.ReadFile(receiptPath)
			if err != nil {
				return fmt.Errorf("read receipt: %w", err)
			}
			var receipt vtp.TaskReceipt
			if err := json.Unmarshal(receiptBytes, &receipt); err != nil {
				return fmt.Errorf("unmarshal receipt: %w", err)
			}

			var spec *vtp.TaskSpec
			if specPath != "" {
				specBytes, err := os.ReadFile(specPath)
				if err != nil {
					return fmt.Errorf("read spec: %w", err)
				}
				spec = &vtp.TaskSpec{}
				if err := json.Unmarshal(specBytes, spec); err != nil {
					return fmt.Errorf("unmarshal spec: %w", err)
				}
			} else {
				spec = &vtp.TaskSpec{
					Protocol: vtp.ProtocolVersion,
					TaskID:   receipt.TaskID,
					Oracle: vtp.OracleSpec{
						Type: "execution@1",
					},
				}
			}

			var actualStdout, actualDiff []byte
			if stdoutPath != "" {
				actualStdout, err = os.ReadFile(stdoutPath)
				if err != nil {
					return fmt.Errorf("read stdout: %w", err)
				}
			}
			if diffPath != "" {
				actualDiff, err = os.ReadFile(diffPath)
				if err != nil {
					return fmt.Errorf("read diff: %w", err)
				}
			}

			if verifierHandle == "" {
				verifierHandle = "agent-memory-verifier"
			}

			verify, err := vtp.VerifyReceipt(spec, &receipt, actualStdout, actualDiff, exitCode, verifierHandle, isDisjoint)
			if err != nil {
				return fmt.Errorf("vtp verify: %w", err)
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(verify); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "VTP Verification Result:\n")
				fmt.Fprintf(cmd.OutOrStdout(), "  Task ID:         %s\n", verify.TaskID)
				fmt.Fprintf(cmd.OutOrStdout(), "  Verdict:         %s\n", verify.Verdict)
				fmt.Fprintf(cmd.OutOrStdout(), "  Basis:           %s\n", verify.Basis)
				fmt.Fprintf(cmd.OutOrStdout(), "  Evidence SHA256: %s\n", verify.EvidenceSHA256)
				fmt.Fprintf(cmd.OutOrStdout(), "  Disjoint Seat:   %t (Clause B)\n", verify.IsDisjointSeat)
			}

			if verify.Verdict != "PASS" {
				return fmt.Errorf("verification failed with verdict %s (%s)", verify.Verdict, verify.Basis)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&receiptPath, "receipt", "", "path to TaskReceipt JSON file (required)")
	cmd.Flags().StringVar(&specPath, "spec", "", "path to TaskSpec JSON file")
	cmd.Flags().StringVar(&stdoutPath, "stdout", "", "path to captured stdout file")
	cmd.Flags().StringVar(&diffPath, "diff", "", "path to captured diff file")
	cmd.Flags().IntVar(&exitCode, "exit-code", 0, "actual process exit code")
	cmd.Flags().StringVar(&verifierHandle, "verifier", "agent-memory-verifier", "handle of verifying agent/seat")
	cmd.Flags().BoolVar(&isDisjoint, "disjoint", false, "assert disjoint seat isolation (Clause B)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit TaskVerify JSON artifact")
	return cmd
}

func newVTPSettleCmd() *cobra.Command {
	var (
		specPath   string
		verifyPath string
		payer      string
		payee      string
		seq        int64
		amount     int
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "settle",
		Short: "Generate a TaskSettle artifact from a passed TaskVerify artifact",
		RunE: func(cmd *cobra.Command, args []string) error {
			if verifyPath == "" {
				return fmt.Errorf("--verify flag is required")
			}
			verifyBytes, err := os.ReadFile(verifyPath)
			if err != nil {
				return fmt.Errorf("read verify artifact: %w", err)
			}
			var verify vtp.TaskVerify
			if err := json.Unmarshal(verifyBytes, &verify); err != nil {
				return fmt.Errorf("unmarshal verify artifact: %w", err)
			}

			var spec *vtp.TaskSpec
			if specPath != "" {
				specBytes, err := os.ReadFile(specPath)
				if err != nil {
					return fmt.Errorf("read spec artifact: %w", err)
				}
				spec = &vtp.TaskSpec{}
				if err := json.Unmarshal(specBytes, spec); err != nil {
					return fmt.Errorf("unmarshal spec artifact: %w", err)
				}
			} else {
				spec = &vtp.TaskSpec{
					Protocol: vtp.ProtocolVersion,
					TaskID:   verify.TaskID,
					Bounty: vtp.BountySpec{
						Currency: "GRN",
						Amount:   amount,
					},
				}
			}

			settle, err := vtp.SettleTask(spec, &verify, payer, payee, seq)
			if err != nil {
				return fmt.Errorf("vtp settle: %w", err)
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(settle)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "VTP Task Settled:\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  Task ID:    %s\n", settle.TaskID)
			fmt.Fprintf(cmd.OutOrStdout(), "  Method:     %s\n", settle.SettlementMethod)
			fmt.Fprintf(cmd.OutOrStdout(), "  Transfer:   %s -> %s (%d %s)\n", settle.Payer, settle.Payee, settle.Amount, spec.Bounty.Currency)
			fmt.Fprintf(cmd.OutOrStdout(), "  ReceiptRef: %s\n", settle.ReceiptRef)
			fmt.Fprintf(cmd.OutOrStdout(), "  SettledSeq: %d\n", settle.SettledSeq)
			return nil
		},
	}
	cmd.Flags().StringVar(&verifyPath, "verify", "", "path to TaskVerify JSON file (required)")
	cmd.Flags().StringVar(&specPath, "spec", "", "path to TaskSpec JSON file")
	cmd.Flags().StringVar(&payer, "payer", "", "payer agent handle")
	cmd.Flags().StringVar(&payee, "payee", "", "payee agent handle")
	cmd.Flags().Int64Var(&seq, "seq", 0, "board sequence number of settlement")
	cmd.Flags().IntVar(&amount, "amount", 1, "bounty amount if spec not provided")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit TaskSettle JSON artifact")
	return cmd
}
