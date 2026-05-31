package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewVersionCmd returns the `agent-memory version` subcommand.
// Output is a single line containing the resolved Version() (release tag,
// the `go install` module version, or "dev (<vcs-rev>)"), written to the
// cobra command's stdout (which can be redirected by callers in tests).
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the agent-memory version and exit",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), Version())
		},
	}
}
