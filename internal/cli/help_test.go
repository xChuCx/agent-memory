package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestKeyCommandsHaveExamples guards the help quality: the root and the
// commands an agent/human reaches for first must carry runnable Examples,
// so `--help` teaches usage instead of only listing flags.
func TestKeyCommandsHaveExamples(t *testing.T) {
	root := NewRootCmd()
	if strings.TrimSpace(root.Example) == "" {
		t.Error("root command has no Example block")
	}

	want := map[string]bool{"fetch": false, "propose": false, "review": false, "apply": false}
	for _, c := range root.Commands() {
		if _, tracked := want[c.Name()]; tracked {
			if strings.TrimSpace(c.Example) == "" {
				t.Errorf("command %q has no Example block", c.Name())
			}
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected command %q not registered", name)
		}
	}
}

// TestRootHelpRendersExamples confirms `--help` actually surfaces the
// Examples section (cobra renders the Example field under "Examples:").
func TestRootHelpRendersExamples(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Examples:") {
		t.Errorf("--help output missing Examples section:\n%s", s)
	}
	if !strings.Contains(s, "agent-memory init") {
		t.Errorf("--help Examples missing the setup flow:\n%s", s)
	}
}
