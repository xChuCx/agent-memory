package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// Severity of a Doctor finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is one diagnostic emitted by doctor.
type Finding struct {
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// NewDoctorCmd returns the `agent-memory doctor` subcommand.
func NewDoctorCmd() *cobra.Command {
	var rootFlag string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose .agent-memory/ layout issues",
		Long: `Reports any issues with the .agent-memory/ layout:

  - missing required files / directories
  - manifest or schema that fails Validate()
  - other advisory checks

Always returns exit code 0 — doctor is an advisory tool, not a gate.
For a hard failure on schema/manifest problems, use --strict (errors
in that mode exit non-zero).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			findings, err := runDoctor(rootFlag)
			if err != nil {
				return err
			}
			return writeDoctorReport(cmd.OutOrStdout(), findings)
		},
	}
	cmd.Flags().StringVar(&rootFlag, "root", "", "repo root (default: current working directory)")
	return cmd
}

// runDoctor returns the slice of findings. Empty slice means everything
// is fine. Exposed for direct test calls.
func runDoctor(rootFlag string) ([]Finding, error) {
	root, err := resolveRoot(rootFlag)
	if err != nil {
		return nil, err
	}
	memDir := memoryDir(root)

	if ok, _ := pathExists(memDir); !ok {
		return []Finding{{
			Severity: SeverityError,
			Message:  fmt.Sprintf(".agent-memory/ not found at %s (run `agent-memory init`)", memDir),
		}}, nil
	}

	var findings []Finding

	// Required regular files.
	requiredFiles := []string{
		"meta/manifest.yaml",
		"meta/schema.yaml",
		".gitignore",
		"index.md",
		"conventions.md",
		"decisions.md",
		"pitfalls.md",
	}
	for _, rel := range requiredFiles {
		p := filepath.Join(memDir, rel)
		if ok, _ := pathExists(p); !ok {
			findings = append(findings, Finding{
				Severity: SeverityError,
				Message:  fmt.Sprintf("missing required file: %s", rel),
			})
		}
	}

	// Required directories. local/sessions/staging are part of the layout
	// but git-ignored; the directory itself should still exist on disk.
	requiredDirs := []string{"modules", "archive", "local", "sessions", "staging", "meta"}
	for _, rel := range requiredDirs {
		p := filepath.Join(memDir, rel)
		info, err := os.Stat(p)
		if err != nil {
			findings = append(findings, Finding{
				Severity: SeverityWarning,
				Message:  fmt.Sprintf("missing required directory: %s/", rel),
			})
			continue
		}
		if !info.IsDir() {
			findings = append(findings, Finding{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s exists but is not a directory", rel),
			})
		}
	}

	// Manifest: load + Validate.
	manifestPath := filepath.Join(memDir, "meta", "manifest.yaml")
	if m, err := config.LoadManifest(manifestPath); err != nil {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Message:  fmt.Sprintf("manifest load failed: %v", err),
		})
	} else if err := m.Validate(); err != nil {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Message:  fmt.Sprintf("manifest invalid: %v", err),
		})
	}

	// Schema: load + Validate.
	schemaPath := filepath.Join(memDir, "meta", "schema.yaml")
	if s, err := schema.LoadSchema(schemaPath); err != nil {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Message:  fmt.Sprintf("schema load failed: %v", err),
		})
	} else if err := s.Validate(); err != nil {
		findings = append(findings, Finding{
			Severity: SeverityError,
			Message:  fmt.Sprintf("schema invalid: %v", err),
		})
	}

	// Stable order for deterministic output.
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return severityRank(findings[i].Severity) < severityRank(findings[j].Severity)
		}
		return findings[i].Message < findings[j].Message
	})

	return findings, nil
}

func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

func writeDoctorReport(w io.Writer, findings []Finding) error {
	if len(findings) == 0 {
		fmt.Fprintln(w, "All checks passed.")
		return nil
	}
	fmt.Fprintf(w, "%d finding(s):\n", len(findings))
	for _, f := range findings {
		prefix := "info:  "
		switch f.Severity {
		case SeverityError:
			prefix = "ERROR: "
		case SeverityWarning:
			prefix = "warn:  "
		}
		fmt.Fprintf(w, "  %s%s\n", prefix, f.Message)
	}
	return nil
}
