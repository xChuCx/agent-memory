// Command agent-memory is the CLI and MCP server entry point for the
// agent-memory project. See agent-memory-design-doc-v0.4.1.md.
package main

import (
	"fmt"
	"os"

	"github.com/agent-memory/agent-memory/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
