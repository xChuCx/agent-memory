package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctor_HappyPath(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}

	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	if len(findings) != 0 {
		for _, f := range findings {
			t.Errorf("unexpected finding [%s]: %s", f.Severity, f.Message)
		}
	}
}

func TestDoctor_ReportsMissingAgentMemoryAsError(t *testing.T) {
	dir := t.TempDir()
	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 finding, got %d", len(findings))
	}
	if findings[0].Severity != SeverityError {
		t.Errorf("severity = %q, want error", findings[0].Severity)
	}
	if !strings.Contains(findings[0].Message, ".agent-memory") {
		t.Errorf("message doesn't mention .agent-memory: %s", findings[0].Message)
	}
}

func TestDoctor_ReportsMissingDurableFile(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	// Remove pitfalls.md to simulate damage.
	if err := os.Remove(filepath.Join(dir, ".agent-memory", "pitfalls.md")); err != nil {
		t.Fatal(err)
	}

	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundMissing := false
	for _, f := range findings {
		if strings.Contains(f.Message, "pitfalls.md") && f.Severity == SeverityError {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Errorf("expected error finding for missing pitfalls.md, got: %+v", findings)
	}
}

func TestDoctor_ReportsMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	// Remove the staging/ directory.
	if err := os.Remove(filepath.Join(dir, ".agent-memory", "staging")); err != nil {
		t.Fatal(err)
	}

	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundMissing := false
	for _, f := range findings {
		if strings.Contains(f.Message, "staging") && f.Severity == SeverityWarning {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Errorf("expected warning for missing staging/, got: %+v", findings)
	}
}

func TestDoctor_ReportsManifestParseError(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the manifest.
	manifestPath := filepath.Join(dir, ".agent-memory", "meta", "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte("not yaml: {{{"), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundParseError := false
	for _, f := range findings {
		if strings.Contains(f.Message, "manifest") && f.Severity == SeverityError {
			foundParseError = true
			break
		}
	}
	if !foundParseError {
		t.Errorf("expected manifest error finding, got: %+v", findings)
	}
}

func TestDoctor_ReportsSchemaParseError(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(dir, ".agent-memory", "meta", "schema.yaml")
	if err := os.WriteFile(schemaPath, []byte("not yaml: {{{"), 0644); err != nil {
		t.Fatal(err)
	}

	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatal(err)
	}
	foundParseError := false
	for _, f := range findings {
		if strings.Contains(f.Message, "schema") && f.Severity == SeverityError {
			foundParseError = true
			break
		}
	}
	if !foundParseError {
		t.Errorf("expected schema error finding, got: %+v", findings)
	}
}

func TestDoctor_FlagsStaleStaging(t *testing.T) {
	// Use sweepFixture (defined in sweep_test.go) — it stages one
	// already-expired proposal under a manifest with TTL=1h.
	root, _, _ := sweepFixture(t)

	findings, err := runDoctor(root)
	if err != nil {
		t.Fatal(err)
	}
	foundStale := false
	for _, f := range findings {
		if strings.Contains(f.Message, "past TTL") && f.Severity == SeverityInfo {
			foundStale = true
			break
		}
	}
	if !foundStale {
		t.Errorf("expected info finding for stale staging, got: %+v", findings)
	}
}

func TestDoctor_NoStaleAdvisoryWhenAllFresh(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "fresh"}); err != nil {
		t.Fatal(err)
	}
	// No proposals staged → no advisory.
	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.Contains(f.Message, "past TTL") {
			t.Errorf("unexpected stale-staging advisory on fresh repo: %+v", f)
		}
	}
}

func TestMCPRootFindings(t *testing.T) {
	repo := t.TempDir()
	other := t.TempDir()
	mk := func(args ...string) []byte {
		b, _ := json.Marshal(map[string]any{
			"mcpServers": map[string]any{
				"agent-memory": map[string]any{"args": args},
			},
		})
		return b
	}
	cases := []struct {
		name string
		data []byte
		warn bool
	}{
		{"pinned elsewhere", mk("mcp", "--root", other), true},
		{"matches repo", mk("mcp", "--root", repo), false},
		{"portable CLAUDE_PROJECT_DIR", mk("mcp", "--root", "${CLAUDE_PROJECT_DIR:-.}"), false},
		{"no --root (env/cwd resolved)", mk("mcp"), false},
		{"other server only", []byte(`{"mcpServers":{"other":{"args":["x"]}}}`), false},
		{"unparseable", []byte("not json"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpRootFindings(repo, []mcpScopeConfig{{scope: "test", data: tc.data}})
			if tc.warn && len(got) == 0 {
				t.Errorf("expected a mis-rooted warning, got none")
			}
			if !tc.warn && len(got) != 0 {
				t.Errorf("expected no findings, got %+v", got)
			}
		})
	}
}

func TestDoctor_FlagsMisrootedProjectMCP(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}
	// A project .mcp.json pinned to some other repo (the 0.5.1 footgun).
	bad := `{"mcpServers":{"agent-memory":{"args":["mcp","--root","/some/other/repo"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	findings, err := runDoctor(dir)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f.Message, "pinned to --root") && f.Severity == SeverityWarning {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a mis-rooted MCP warning, got: %+v", findings)
	}
}

func TestDoctor_HumanOutput_AllPassed(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "p"}); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"doctor", "--root", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out.String(), "All checks passed") {
		t.Errorf("expected 'All checks passed' on healthy layout, got: %s", out.String())
	}
}

func TestDoctor_HumanOutput_ListsFindings(t *testing.T) {
	dir := t.TempDir() // no init

	out := &bytes.Buffer{}
	root := NewRootCmd()
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"doctor", "--root", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	output := out.String()
	if !strings.Contains(output, "finding(s)") {
		t.Errorf("expected finding count, got: %s", output)
	}
	if !strings.Contains(output, "ERROR") {
		t.Errorf("expected ERROR prefix for missing .agent-memory, got: %s", output)
	}
}
