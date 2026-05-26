package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionCommand runs the root command with args ["version"] and asserts
// the output is exactly ProgramVersion (plus trailing newline).
func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	root := NewRootCmd()
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := strings.TrimRight(stdout.String(), "\r\n")
	if got != ProgramVersion {
		t.Errorf("version stdout = %q, want %q", got, ProgramVersion)
	}
	if stderr.Len() != 0 {
		t.Errorf("version stderr non-empty: %q", stderr.String())
	}
}

// TestProgramVersionConstant guards against accidental empties.
func TestProgramVersionConstant(t *testing.T) {
	if ProgramVersion == "" {
		t.Fatal("ProgramVersion is empty")
	}
	if !strings.HasPrefix(ProgramVersion, "0.") {
		t.Errorf("ProgramVersion %q doesn't start with 0.", ProgramVersion)
	}
}

// TestRootCmdHasSubcommands sanity-checks that subcommand registration works.
func TestRootCmdHasSubcommands(t *testing.T) {
	root := NewRootCmd()
	if _, _, err := root.Find([]string{"version"}); err != nil {
		t.Errorf("version subcommand not found: %v", err)
	}
}
