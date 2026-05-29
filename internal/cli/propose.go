package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/agent-memory/agent-memory/internal/config"
	"github.com/agent-memory/agent-memory/internal/index"
	"github.com/agent-memory/agent-memory/internal/memory"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// proposeFlags collects the propose command's flag values.
type proposeFlags struct {
	root      string
	fromJSON  string // path to a full ProposeRequest JSON ("-" = stdin)
	asJSON    bool
	autoApply bool

	// Request-level (flag mode).
	intent     string
	rationale  string
	confidence string
	sources    []string // "type:ref" pairs

	// Single-operation (flag mode).
	op              string
	path            string
	sectionID       string
	heading         string
	headingLevel    int
	occurrence      int
	parentSectionID string
	content         string
	contentFile     string // path to read Content from ("-" = stdin)
	ifExists        string
	ifMissing       string
	archivePath     string
	replacement     string
	reason          string
	newHeading      string
	newHeadingLevel int
}

// ProposeReport is the JSON shape for `propose --json`: the orchestrator's
// response, plus the apply outcome when --apply landed a staged proposal.
type ProposeReport struct {
	*memory.ProposeResponse
	Applied *memory.ApplyResult `json:"applied,omitempty"`
}

// NewProposeCmd returns the `agent-memory propose` subcommand: a human-facing
// front door to the same memory.ProposeUpdate pipeline the MCP
// memory.propose_update tool uses, so proposals can be created without an
// MCP server running.
func NewProposeCmd() *cobra.Command {
	var f proposeFlags
	cmd := &cobra.Command{
		Use:   "propose",
		Short: "Create a memory proposal (the CLI front door to propose_update)",
		Long: `Create a structured memory proposal and run it through the same
validation / secret-scan / routing pipeline the MCP memory.propose_update
tool uses — no MCP server required.

Two ways to specify the proposal:

  - Flags, for the common single-operation case:
      agent-memory propose --intent add_pitfall \
        --op append_to_section --path pitfalls.md \
        --section-id lock-ordering --content "- always lock A before B\n"

  - --from-json <file|->, for full control (multiple operations, exact
    field values). The JSON is a ProposeRequest:
      {"intent":"record_decision","sources":[{"type":"user","ref":"..."}],
       "confidence":"confirmed","operations":[{"operation":"append_section",
       "path":"decisions.md","heading":"...","heading_level":2,
       "content":"## ...\n<!-- @id: ... -->\n..."}]}

Content can come from --content (literal), --content-file <path>, or
--content-file - (stdin).

Routing is decided by the server per category: durable categories
(decisions, modules, conventions, archive) STAGE for review; local/session
notes and pitfall appends APPLY. Pass --apply to immediately apply a result
that would otherwise stage — you are the reviewer when you run this command.

Exit status is non-zero when the proposal is rejected.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := runPropose(cmd.Context(), &f, cmd.InOrStdin())
			if err != nil {
				return err
			}
			if f.asJSON {
				if err := writeJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else if err := writeProposeHuman(cmd.OutOrStdout(), report); err != nil {
				return err
			}
			// Non-zero exit on a rejected proposal so scripts can fail fast.
			if report.Status == memory.StatusRejected {
				return fmt.Errorf("proposal rejected: %s", report.Reason)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.root, "root", "", "repo root (default: current working directory)")
	fl.StringVar(&f.fromJSON, "from-json", "", "read a full ProposeRequest from this file (- = stdin); overrides the flag-based operation")
	fl.BoolVar(&f.asJSON, "json", false, "emit the result as JSON")
	fl.BoolVar(&f.autoApply, "apply", false, "immediately apply a result that would otherwise stage (you are the reviewer)")

	fl.StringVar(&f.intent, "intent", "", "intent: update_current|update_shared|session_log|add_pitfall|record_decision|refresh_module|update_conventions|archive_stale")
	fl.StringVar(&f.rationale, "rationale", "", "short human-readable reason (shown in status / staging slug)")
	fl.StringVar(&f.confidence, "confidence", "", "confirmed|inferred|user-provided|stale|unknown")
	fl.StringArrayVar(&f.sources, "source", nil, "provenance source as type:ref (repeatable), e.g. file:internal/x.go")

	fl.StringVar(&f.op, "op", "", "operation: create_file|replace_section|append_section|append_to_section|replace_section_content|archive_section|remove_section|rename_heading")
	fl.StringVar(&f.path, "path", "", "target path under .agent-memory/ (forward-slash)")
	fl.StringVar(&f.sectionID, "section-id", "", "target section @id")
	fl.StringVar(&f.heading, "heading", "", "heading text (for new/append/replace section)")
	fl.IntVar(&f.headingLevel, "heading-level", 0, "heading level (1-6)")
	fl.IntVar(&f.occurrence, "occurrence", 0, "1-based occurrence to disambiguate duplicate headings")
	fl.StringVar(&f.parentSectionID, "parent-section-id", "", "parent section @id for append_section")
	fl.StringVar(&f.content, "content", "", "literal content")
	fl.StringVar(&f.contentFile, "content-file", "", "read content from this file (- = stdin)")
	fl.StringVar(&f.ifExists, "if-exists", "", "create_file: reject|append|replace")
	fl.StringVar(&f.ifMissing, "if-missing", "", "replace_section: reject|append")
	fl.StringVar(&f.archivePath, "archive-path", "", "archive_section/remove_section: new archive/ path")
	fl.StringVar(&f.replacement, "replacement", "", "archive_section: replacement stub body")
	fl.StringVar(&f.reason, "reason", "", "remove_section: why it's gone")
	fl.StringVar(&f.newHeading, "new-heading", "", "rename_heading: new heading text")
	fl.IntVar(&f.newHeadingLevel, "new-heading-level", 0, "rename_heading: new level (0 = keep)")
	return cmd
}

// runPropose builds a ProposeRequest (from --from-json or flags), runs
// ProposeUpdate, and — when --apply is set and the result staged — applies it.
func runPropose(ctx context.Context, f *proposeFlags, stdin io.Reader) (*ProposeReport, error) {
	memDir, err := reviewMemDir(f.root)
	if err != nil {
		return nil, err
	}
	manifest, err := config.LoadManifest(filepath.Join(memDir, "meta", "manifest.yaml"))
	if err != nil {
		return nil, fmt.Errorf("propose: load manifest: %w", err)
	}
	sch, err := schema.LoadSchema(filepath.Join(memDir, "meta", "schema.yaml"))
	if err != nil {
		return nil, fmt.Errorf("propose: load schema: %w", err)
	}
	idx, err := index.Open(filepath.Join(memDir, "meta", "index.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("propose: open index: %w", err)
	}
	defer func() { _ = idx.Close() }()
	if err := idx.Init(ctx); err != nil {
		return nil, fmt.Errorf("propose: init index: %w", err)
	}

	req, err := f.buildRequest(stdin)
	if err != nil {
		return nil, err
	}

	deps := memory.UpdateDeps{
		Manifest:  manifest,
		Schema:    sch,
		MemoryDir: memDir,
		Idx:       idx,
		Logger:    cliLogger(),
	}
	resp, err := memory.ProposeUpdate(ctx, req, deps)
	if err != nil {
		return nil, fmt.Errorf("propose: %w", err)
	}
	report := &ProposeReport{ProposeResponse: resp}

	// --apply: the human running the CLI is the reviewer. If routing staged
	// the proposal, apply it now (drift re-check + index update + git
	// auto-stage all run via the normal apply path).
	if f.autoApply && resp.Status == memory.StatusStaged && resp.StagingID != "" {
		applied, err := memory.ApplyStaged(ctx, resp.StagingID, deps)
		if err != nil {
			return nil, fmt.Errorf("propose --apply: %w", err)
		}
		report.Applied = applied
	}
	return report, nil
}

// buildRequest assembles the ProposeRequest from --from-json or the flag set.
func (f *proposeFlags) buildRequest(stdin io.Reader) (memory.ProposeRequest, error) {
	if f.fromJSON != "" {
		raw, err := readSource(f.fromJSON, stdin)
		if err != nil {
			return memory.ProposeRequest{}, fmt.Errorf("propose: read --from-json: %w", err)
		}
		var req memory.ProposeRequest
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return memory.ProposeRequest{}, fmt.Errorf("propose: parse --from-json: %w", err)
		}
		if req.Owner.Kind == "" {
			req.Owner.Kind = "cli"
		}
		return req, nil
	}

	if f.intent == "" {
		return memory.ProposeRequest{}, fmt.Errorf("propose: --intent is required (or use --from-json)")
	}
	if f.op == "" {
		return memory.ProposeRequest{}, fmt.Errorf("propose: --op is required (or use --from-json)")
	}
	if f.content != "" && f.contentFile != "" {
		return memory.ProposeRequest{}, fmt.Errorf("propose: pass --content or --content-file, not both")
	}
	content := f.content
	if f.contentFile != "" {
		raw, err := readSource(f.contentFile, stdin)
		if err != nil {
			return memory.ProposeRequest{}, fmt.Errorf("propose: read --content-file: %w", err)
		}
		content = string(raw)
	}
	sources, err := parseSources(f.sources)
	if err != nil {
		return memory.ProposeRequest{}, err
	}

	return memory.ProposeRequest{
		Intent:     memory.Intent(f.intent),
		Rationale:  f.rationale,
		Confidence: f.confidence,
		Sources:    sources,
		Owner:      memory.OwnerInfo{Kind: "cli"},
		Operations: []memory.OperationInput{{
			Op:              f.op,
			Path:            f.path,
			SectionID:       f.sectionID,
			Heading:         f.heading,
			HeadingLevel:    f.headingLevel,
			Occurrence:      f.occurrence,
			ParentSectionID: f.parentSectionID,
			Content:         content,
			IfExists:        f.ifExists,
			IfMissing:       f.ifMissing,
			ArchivePath:     f.archivePath,
			Replacement:     f.replacement,
			Reason:          f.reason,
			NewHeading:      f.newHeading,
			NewHeadingLevel: f.newHeadingLevel,
		}},
	}, nil
}

// parseSources turns "type:ref" flag values into []memory.Source. A value
// with no colon is taken as a bare type with an empty ref.
func parseSources(raw []string) ([]memory.Source, error) {
	var out []memory.Source
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		typ, ref, found := strings.Cut(s, ":")
		typ = strings.TrimSpace(typ)
		if typ == "" {
			return nil, fmt.Errorf("propose: invalid --source %q (want type:ref)", s)
		}
		src := memory.Source{Type: typ}
		if found {
			src.Ref = strings.TrimSpace(ref)
		}
		out = append(out, src)
	}
	return out, nil
}

// readSource reads from path, or from stdin when path is "-".
func readSource(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

func writeProposeHuman(w io.Writer, r *ProposeReport) error {
	switch r.Status {
	case memory.StatusApplied:
		fmt.Fprintf(w, "Applied (%s):\n", r.Routing.Mode)
		for _, f := range r.Files {
			fmt.Fprintf(w, "  wrote: %s\n", f)
		}
	case memory.StatusStaged:
		if r.Applied != nil {
			fmt.Fprintf(w, "Staged %s, then applied via --apply:\n", r.StagingID)
			for _, f := range r.Applied.Files {
				fmt.Fprintf(w, "  wrote: %s\n", f)
			}
			if r.Applied.Status != memory.StatusApplied {
				fmt.Fprintf(w, "  apply did NOT complete: %s — %s\n", r.Applied.Reason, r.Applied.Message)
			}
		} else {
			fmt.Fprintf(w, "Staged %s (human approval required).\n", r.StagingID)
			fmt.Fprintf(w, "  inspect: agent-memory review %s --diff\n", r.StagingID)
			fmt.Fprintf(w, "  apply:   agent-memory apply %s\n", r.StagingID)
		}
	default: // rejected
		fmt.Fprintf(w, "Rejected: %s\n", r.Reason)
		if r.Message != "" {
			fmt.Fprintf(w, "  %s\n", r.Message)
		}
		for _, f := range r.Findings {
			fmt.Fprintf(w, "  secret: %s at %s\n", f.Type, f.ApproximateLocation)
		}
		for _, v := range r.ProvenanceViolations {
			fmt.Fprintf(w, "  provenance: %s\n", v)
		}
		for _, v := range r.Violations {
			fmt.Fprintf(w, "  schema: %s\n", v.Message)
		}
	}
	return nil
}
