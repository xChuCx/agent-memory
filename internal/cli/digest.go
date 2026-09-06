package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/memory"
)

// NewDigestCmd returns the `agent-memory digest` subcommand.
func NewDigestCmd() *cobra.Command {
	var (
		rootFlag   string
		verifyFlag string
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "digest",
		Short: "Compute or verify deterministic SHA-256 Merkle root of active memory",
		Long: `Computes a deterministic SHA-256 Merkle root across all active durable
markdown files (.agent-memory/conventions.md, decisions.md, index.md,
pitfalls.md, and modules/*.md).

Normalizes line endings (\r\n -> \n) for cross-platform parity.
Use --verify <sha256> to assert the current memory state matches an expected
hash (exits non-zero on mismatch).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveRoot(rootFlag)
			if err != nil {
				return err
			}
			memDir := memoryDir(root)
			if ok, _ := pathExists(memDir); !ok {
				return fmt.Errorf(".agent-memory/ not found at %s (run `agent-memory init`)", memDir)
			}

			d, err := memory.ComputeDigest(memDir)
			if err != nil {
				return fmt.Errorf("digest: %w", err)
			}

			if verifyFlag != "" {
				verifyClean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(verifyFlag)), "sha256:")
				actualClean := strings.ToLower(d.RootHash)
				if verifyClean != actualClean {
					return fmt.Errorf("merkle digest mismatch: expected %s, got %s", verifyClean, actualClean)
				}
			}

			return writeDigestReport(cmd.OutOrStdout(), d, asJSON, verifyFlag != "")
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	cmd.Flags().StringVar(&verifyFlag, "verify", "", "assert current state matches expected sha256 root")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON matching agent_memory_digest/1 schema")
	return cmd
}

func writeDigestReport(out io.Writer, d *memory.MemoryDigest, asJSON bool, verified bool) error {
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	}

	if verified {
		fmt.Fprintf(out, "OK: Merkle root verified (%s)\n", d.RootHash)
	} else {
		fmt.Fprintf(out, "Active Memory Merkle Root: %s\n", d.RootHash)
	}
	fmt.Fprintf(out, "Files: %d | Total Bytes: %d\n", d.FilesCount, d.TotalBytes)
	if len(d.Leaves) > 0 {
		fmt.Fprintln(out, "Leaves:")
		for _, l := range d.Leaves {
			fmt.Fprintf(out, "  %-20s %s (%d B)\n", l.Path, l.SHA256, l.Bytes)
		}
	}
	return nil
}
