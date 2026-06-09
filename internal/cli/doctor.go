package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/xChuCx/agent-memory/internal/config"
	"github.com/xChuCx/agent-memory/internal/memory"
	"github.com/xChuCx/agent-memory/internal/schema"
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

	// Manifest: load + Validate. We hold the loaded manifest below for the
	// staging-TTL check, but only if Validate succeeded.
	manifestPath := filepath.Join(memDir, "meta", "manifest.yaml")
	var manifest *config.Manifest
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
	} else {
		manifest = m
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

	// Stale staged proposals: count how many sit past the manifest TTL
	// and emit an advisory `info` finding. Doctor never auto-sweeps;
	// it just nudges the user toward `agent-memory sweep`.
	if manifest != nil && manifest.Staging.TTLSeconds > 0 {
		ttl := time.Duration(manifest.Staging.TTLSeconds) * time.Second
		if res, err := memory.SweepStale(memDir, ttl, true); err == nil && len(res.Expired) > 0 {
			findings = append(findings, Finding{
				Severity: SeverityInfo,
				Message: fmt.Sprintf(
					"%d staged proposal(s) past TTL (%s); run `agent-memory sweep` to remove",
					len(res.Expired), ttl,
				),
			})
		}
	}

	// MCP registration sanity: a project .mcp.json whose agent-memory server is
	// pinned to a fixed --root that isn't this repo silently routes the agent's
	// memory writes to the wrong project (the 0.5.1 footgun). Best-effort, and
	// scoped to the project file so doctor stays hermetic — a portable
	// ${CLAUDE_PROJECT_DIR...} root or an absent --root is never flagged.
	if b, rerr := os.ReadFile(filepath.Join(root, ".mcp.json")); rerr == nil {
		findings = append(findings, mcpRootFindings(root, []mcpScopeConfig{{scope: ".mcp.json", data: b}})...)
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

// mcpScopeConfig is one MCP config source (a scope label + its raw JSON).
type mcpScopeConfig struct {
	scope string
	data  []byte
}

// mcpRootFindings flags an `agent-memory` MCP server whose fixed `--root`
// points somewhere other than this repo — the misconfiguration that silently
// routes the agent's memory writes to the wrong project. A `--root` that uses
// `${CLAUDE_PROJECT_DIR...}` (the portable form `install` writes) or is absent
// (resolved from the environment / cwd at spawn) is correct and never flagged.
// Parsing is best-effort: an unparseable config yields no finding.
func mcpRootFindings(repoRoot string, configs []mcpScopeConfig) []Finding {
	repoAbs, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil
	}
	var out []Finding
	for _, c := range configs {
		var doc struct {
			MCPServers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(c.data, &doc) != nil {
			continue
		}
		srv, ok := doc.MCPServers["agent-memory"]
		if !ok {
			continue
		}
		rootArg, hasRoot := flagValue(srv.Args, "--root")
		if !hasRoot || strings.Contains(rootArg, "${") {
			continue // env-resolved or portable → correct
		}
		ra, err := filepath.Abs(rootArg)
		if err != nil {
			continue
		}
		if !strings.EqualFold(filepath.Clean(ra), filepath.Clean(repoAbs)) {
			out = append(out, Finding{
				Severity: SeverityWarning,
				Message: fmt.Sprintf(
					"MCP server 'agent-memory' in %s is pinned to --root %q, not this repo (%s); the agent's memory writes will land in the wrong project. Re-run `agent-memory install claude` here, or fix the --root.",
					c.scope, rootArg, repoAbs),
			})
		}
	}
	return out
}

// flagValue returns the argument following flag in args, if present.
func flagValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
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
