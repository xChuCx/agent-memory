package cli

import (
	"bytes"
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
