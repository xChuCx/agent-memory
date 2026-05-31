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
	if got != Version() {
		t.Errorf("version stdout = %q, want %q", got, Version())
	}
	if stderr.Len() != 0 {
		t.Errorf("version stderr non-empty: %q", stderr.String())
	}
}

// TestProgramVersion_Set guards against accidental empties. The value
// is either the "dev" default (regular `go build`) or a semver-shaped
// string injected by goreleaser via -ldflags. Tests accept both shapes
// so neither dev nor release builds break the suite.
func TestProgramVersion_Set(t *testing.T) {
	if ProgramVersion == "" {
		t.Fatal("ProgramVersion is empty")
	}
	if ProgramVersion == "dev" {
		return // default; valid
	}
	// Accept "vX.Y.Z" or "X.Y.Z" — both are valid semver shapes that
	// goreleaser might stamp depending on configuration.
	v := strings.TrimPrefix(ProgramVersion, "v")
	if !strings.HasPrefix(v, "0.") && !strings.HasPrefix(v, "1.") {
		t.Errorf("ProgramVersion = %q; not 'dev' and not a valid 0.x / 1.x semver", ProgramVersion)
	}
}

// TestRootCmdHasSubcommands sanity-checks that subcommand registration works.
func TestRootCmdHasSubcommands(t *testing.T) {
	root := NewRootCmd()
	if _, _, err := root.Find([]string{"version"}); err != nil {
		t.Errorf("version subcommand not found: %v", err)
	}
}
