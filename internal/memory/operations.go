package memory

import (
	"bytes"
	"errors"
	"fmt"

	agentmd "github.com/agent-memory/agent-memory/internal/markdown"
	"github.com/agent-memory/agent-memory/internal/schema"
)

// DriftPolicy controls how the staging engine (M5) re-validates an
// operation's target at apply time. See design doc v0.4.1 §16.4.
type DriftPolicy int

const (
	// RequireSectionContentMatch — used by replace/archive/remove/rename
	// where the operation is only meaningful against a specific snapshot
	// of the target section. Drift = section's content hash changed.
	RequireSectionContentMatch DriftPolicy = iota

	// RequireSectionResolvable — used by append_to_section. The section
	// may have grown since staging; we only require it to still resolve
	// by its ID.
	RequireSectionResolvable

	// RequireFileAbsent — used by create_file with if_exists=reject.
	// Drift = file appeared between stage and apply.
	RequireFileAbsent

	// RequireFilePresent — used by create_file with if_exists=append/replace,
	// and by append_section against a parent_section_id.
	RequireFilePresent
)

// OperationTarget describes a single (file, optional section) the operation
// depends on for its drift check. The orchestrator (T3.7) materialises
// Hash from disk at staging time.
type OperationTarget struct {
	Path      string      `json:"path"`
	SectionID string      `json:"section_id,omitempty"`
	Policy    DriftPolicy `json:"policy"`
	Hash      string      `json:"hash,omitempty"`
}

// Operation is one structured Markdown edit. Concrete types live in this
// file (T3.2). Construct via ParseOperation from an OperationInput, or
// directly with a struct literal in tests.
type Operation interface {
	// Kind returns the operation type ("create_file", "replace_section", ...).
	Kind() string

	// Path returns the target memory-relative file path (forward-slash).
	Path() string

	// Validate runs op-specific structural checks: content parses as
	// Markdown, required fields are present, paths are well-formed.
	// The schema is passed so an op can consult per-category policy.
	// Category-level checks (server_managed, agent_writable) are the
	// orchestrator's responsibility.
	Validate(sch *schema.Schema) error

	// Targets returns the drift-check targets the staging engine should
	// verify at apply time. May be empty for ops with no drift concern.
	Targets() []OperationTarget

	// Plan returns the byte-range splice that, applied via
	// markdown.Splice(src, []SpliceOp{plan}), produces the desired
	// post-state. For create_file with if_exists=reject, src is expected
	// to be nil (file doesn't exist) and the splice covers [0, 0).
	Plan(src []byte) (agentmd.SpliceOp, error)
}

// OperationInput is the JSON shape every operation deserialises from.
// Fields not relevant to a given op are omitted (omitempty); ParseOperation
// validates required fields per op type.
type OperationInput struct {
	Op              string `json:"operation"`
	Path            string `json:"path"`
	SectionID       string `json:"section_id,omitempty"`
	Heading         string `json:"heading,omitempty"`
	HeadingLevel    int    `json:"heading_level,omitempty"`
	Occurrence      int    `json:"occurrence,omitempty"`
	ParentSectionID string `json:"parent_section_id,omitempty"`
	Content         string `json:"content,omitempty"`
	IfExists        string `json:"if_exists,omitempty"`
	IfMissing       string `json:"if_missing,omitempty"`
}

// ParseOperation dispatches on in.Op to construct a concrete Operation.
// Returns a clear error for unknown op kinds.
func ParseOperation(in OperationInput) (Operation, error) {
	switch in.Op {
	case "create_file":
		return &CreateFile{
			FilePath: in.Path,
			Content:  []byte(in.Content),
			IfExists: in.IfExists,
		}, nil
	case "replace_section":
		return &ReplaceSection{
			FilePath:   in.Path,
			SectionID:  in.SectionID,
			Heading:    in.Heading,
			Level:      in.HeadingLevel,
			Occurrence: in.Occurrence,
			Content:    []byte(in.Content),
			IfMissing:  in.IfMissing,
		}, nil
	case "append_section":
		return &AppendSection{
			FilePath:        in.Path,
			ParentSectionID: in.ParentSectionID,
			Heading:         in.Heading,
			Level:           in.HeadingLevel,
			Content:         []byte(in.Content),
		}, nil
	case "append_to_section":
		return &AppendToSection{
			FilePath:   in.Path,
			SectionID:  in.SectionID,
			Heading:    in.Heading,
			Level:      in.HeadingLevel,
			Occurrence: in.Occurrence,
			Content:    []byte(in.Content),
		}, nil
	case "replace_section_content":
		return &ReplaceSectionContent{
			FilePath:   in.Path,
			SectionID:  in.SectionID,
			Heading:    in.Heading,
			Level:      in.HeadingLevel,
			Occurrence: in.Occurrence,
			Content:    []byte(in.Content),
		}, nil
	default:
		return nil, fmt.Errorf("unknown operation kind: %q", in.Op)
	}
}

// resolveSection looks up a section by ID (preferred) or by
// heading+level+occurrence (fallback). Used by ReplaceSection /
// AppendToSection / ReplaceSectionContent. Returns a typed error path
// that the caller can map to user-facing messages.
func resolveSection(src []byte, sectionID, heading string, level, occurrence int) (*agentmd.Section, error) {
	sections, err := agentmd.ParseSections(src)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if sectionID != "" {
		sec, ok := agentmd.FindByID(sections, sectionID)
		if !ok {
			return nil, fmt.Errorf("section not found by id: %q", sectionID)
		}
		return sec, nil
	}
	if heading == "" {
		return nil, errors.New("section_id or heading is required")
	}
	occ := occurrence
	if occ == 0 {
		occ = 1
	}
	sec, ok := agentmd.FindByHeading(sections, heading, level, occ)
	if !ok {
		return nil, fmt.Errorf("section not found by heading: %q level=%d occurrence=%d", heading, level, occ)
	}
	return sec, nil
}

// ============================================================================
// CreateFile
// ============================================================================

// CreateFile creates a new file with Content at FilePath. IfExists controls
// what to do when the file already exists at apply time:
//
//   "reject" (default) — fail; the orchestrator emits "target_drift".
//   "append"           — append Content to the existing file.
//   "replace"          — overwrite the file with Content.
type CreateFile struct {
	FilePath string
	Content  []byte
	IfExists string
}

func (op *CreateFile) Kind() string { return "create_file" }
func (op *CreateFile) Path() string { return op.FilePath }

func (op *CreateFile) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("create_file: path is required")
	}
	if len(op.Content) == 0 {
		return errors.New("create_file: content is required")
	}
	if err := agentmd.ValidateMarkdown(op.Content); err != nil {
		return fmt.Errorf("create_file: content does not parse as Markdown: %w", err)
	}
	switch op.IfExists {
	case "", "reject", "append", "replace":
	default:
		return fmt.Errorf("create_file: invalid if_exists value %q (allowed: reject, append, replace)", op.IfExists)
	}
	return nil
}

func (op *CreateFile) Targets() []OperationTarget {
	policy := RequireFileAbsent
	if op.IfExists == "append" || op.IfExists == "replace" {
		policy = RequireFilePresent
	}
	return []OperationTarget{{Path: op.FilePath, Policy: policy}}
}

func (op *CreateFile) Plan(src []byte) (agentmd.SpliceOp, error) {
	switch op.IfExists {
	case "", "reject":
		// File must not exist; src should be empty/nil. We splice into an
		// empty buffer, which the Splice primitive handles correctly
		// (out = "" + content + "" = content).
		if len(src) > 0 {
			return agentmd.SpliceOp{}, fmt.Errorf("create_file: file already exists and if_exists=reject")
		}
		return agentmd.SpliceOp{ByteStart: 0, ByteEnd: 0, Replacement: op.Content}, nil
	case "append":
		return agentmd.SpliceOp{ByteStart: len(src), ByteEnd: len(src), Replacement: op.Content}, nil
	case "replace":
		return agentmd.SpliceOp{ByteStart: 0, ByteEnd: len(src), Replacement: op.Content}, nil
	}
	return agentmd.SpliceOp{}, fmt.Errorf("create_file: unreachable if_exists branch %q", op.IfExists)
}

// ============================================================================
// ReplaceSection
// ============================================================================

// ReplaceSection replaces the entire section identified by SectionID (or
// Heading+Level+Occurrence) with Content. Content must start with the same
// heading line and include the @id anchor (if the original had one).
type ReplaceSection struct {
	FilePath   string
	SectionID  string
	Heading    string
	Level      int
	Occurrence int
	Content    []byte
	IfMissing  string // "reject" (default) | "append" | "create_file"
}

func (op *ReplaceSection) Kind() string { return "replace_section" }
func (op *ReplaceSection) Path() string { return op.FilePath }

func (op *ReplaceSection) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("replace_section: path is required")
	}
	if op.SectionID == "" && op.Heading == "" {
		return errors.New("replace_section: section_id or heading is required")
	}
	if len(op.Content) == 0 {
		return errors.New("replace_section: content is required")
	}
	if err := agentmd.ValidateMarkdown(op.Content); err != nil {
		return fmt.Errorf("replace_section: %w", err)
	}
	switch op.IfMissing {
	case "", "reject", "append", "create_file":
	default:
		return fmt.Errorf("replace_section: invalid if_missing value %q", op.IfMissing)
	}
	return nil
}

func (op *ReplaceSection) Targets() []OperationTarget {
	return []OperationTarget{{
		Path:      op.FilePath,
		SectionID: op.SectionID,
		Policy:    RequireSectionContentMatch,
	}}
}

func (op *ReplaceSection) Plan(src []byte) (agentmd.SpliceOp, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		switch op.IfMissing {
		case "append":
			return agentmd.SpliceOp{
				ByteStart:   len(src),
				ByteEnd:     len(src),
				Replacement: op.Content,
			}, nil
		case "create_file":
			// The orchestrator handles file creation as a higher-level
			// fallback; report missing to it.
			return agentmd.SpliceOp{}, fmt.Errorf("replace_section: if_missing=create_file should be handled by orchestrator: %w", err)
		default:
			return agentmd.SpliceOp{}, fmt.Errorf("replace_section: %w", err)
		}
	}
	return agentmd.SpliceOp{
		ByteStart:   sec.ByteStart,
		ByteEnd:     sec.ByteEnd,
		Replacement: op.Content,
	}, nil
}

// ============================================================================
// AppendSection
// ============================================================================

// AppendSection adds a new section to the file. If ParentSectionID is set,
// the new section is inserted at the end of that parent (before the next
// sibling/ancestor heading). Otherwise it goes at the end of the file.
//
// Content must start with the heading line. The orchestrator's
// AssignMissingIDs pass will inject an @id anchor on the next index pass
// if Content doesn't carry one.
type AppendSection struct {
	FilePath        string
	ParentSectionID string
	Heading         string
	Level           int
	Content         []byte
}

func (op *AppendSection) Kind() string { return "append_section" }
func (op *AppendSection) Path() string { return op.FilePath }

func (op *AppendSection) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("append_section: path is required")
	}
	if len(op.Content) == 0 {
		return errors.New("append_section: content is required")
	}
	if op.Heading == "" {
		return errors.New("append_section: heading is required")
	}
	if op.Level < 1 || op.Level > 6 {
		return fmt.Errorf("append_section: heading_level must be 1-6, got %d", op.Level)
	}
	if err := agentmd.ValidateMarkdown(op.Content); err != nil {
		return fmt.Errorf("append_section: %w", err)
	}
	return nil
}

func (op *AppendSection) Targets() []OperationTarget {
	t := OperationTarget{Path: op.FilePath, Policy: RequireFilePresent}
	if op.ParentSectionID != "" {
		t.SectionID = op.ParentSectionID
		t.Policy = RequireSectionResolvable
	}
	return []OperationTarget{t}
}

func (op *AppendSection) Plan(src []byte) (agentmd.SpliceOp, error) {
	if op.ParentSectionID == "" {
		// Append at end of file. Make sure there's a newline separating
		// the new heading from preceding content.
		insert := op.Content
		if len(src) > 0 && src[len(src)-1] != '\n' {
			insert = append([]byte("\n"), insert...)
		}
		return agentmd.SpliceOp{
			ByteStart:   len(src),
			ByteEnd:     len(src),
			Replacement: insert,
		}, nil
	}
	parent, err := resolveSection(src, op.ParentSectionID, "", 0, 0)
	if err != nil {
		return agentmd.SpliceOp{}, fmt.Errorf("append_section: parent: %w", err)
	}
	return agentmd.SpliceOp{
		ByteStart:   parent.ByteEnd,
		ByteEnd:     parent.ByteEnd,
		Replacement: op.Content,
	}, nil
}

// ============================================================================
// AppendToSection
// ============================================================================

// AppendToSection appends Content to the END of an existing section (just
// before the next heading at same/higher level). Heading stays untouched.
// Used for bullet-level entries (pitfalls, session logs).
type AppendToSection struct {
	FilePath   string
	SectionID  string
	Heading    string
	Level      int
	Occurrence int
	Content    []byte
}

func (op *AppendToSection) Kind() string { return "append_to_section" }
func (op *AppendToSection) Path() string { return op.FilePath }

func (op *AppendToSection) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("append_to_section: path is required")
	}
	if op.SectionID == "" && op.Heading == "" {
		return errors.New("append_to_section: section_id or heading is required")
	}
	if len(op.Content) == 0 {
		return errors.New("append_to_section: content is required")
	}
	return nil
}

func (op *AppendToSection) Targets() []OperationTarget {
	return []OperationTarget{{
		Path:      op.FilePath,
		SectionID: op.SectionID,
		Policy:    RequireSectionResolvable, // weaker: content may have grown
	}}
}

func (op *AppendToSection) Plan(src []byte) (agentmd.SpliceOp, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return agentmd.SpliceOp{}, fmt.Errorf("append_to_section: %w", err)
	}
	// Section ends at byte before next heading (or EOF). The trailing
	// newline of the section's last line is already there; insert at
	// ByteEnd places content cleanly between sections.
	return agentmd.SpliceOp{
		ByteStart:   sec.ByteEnd,
		ByteEnd:     sec.ByteEnd,
		Replacement: op.Content,
	}, nil
}

// ============================================================================
// ReplaceSectionContent
// ============================================================================

// ReplaceSectionContent replaces a section's BODY only — the heading line
// and the immediately-following @id anchor (if any) and at most one blank
// line after the anchor are preserved. Useful when the heading/ID must stay
// stable but the body changes.
type ReplaceSectionContent struct {
	FilePath   string
	SectionID  string
	Heading    string
	Level      int
	Occurrence int
	Content    []byte // body only; must NOT start with a heading
}

func (op *ReplaceSectionContent) Kind() string { return "replace_section_content" }
func (op *ReplaceSectionContent) Path() string { return op.FilePath }

func (op *ReplaceSectionContent) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("replace_section_content: path is required")
	}
	if op.SectionID == "" && op.Heading == "" {
		return errors.New("replace_section_content: section_id or heading is required")
	}
	if len(op.Content) == 0 {
		return errors.New("replace_section_content: content is required")
	}
	// Content must NOT start with a heading marker.
	if bytes.HasPrefix(bytes.TrimLeft(op.Content, " \t"), []byte("#")) {
		return errors.New("replace_section_content: content must not start with a heading (use replace_section to replace the heading too)")
	}
	return nil
}

func (op *ReplaceSectionContent) Targets() []OperationTarget {
	return []OperationTarget{{
		Path:      op.FilePath,
		SectionID: op.SectionID,
		Policy:    RequireSectionContentMatch,
	}}
}

func (op *ReplaceSectionContent) Plan(src []byte) (agentmd.SpliceOp, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return agentmd.SpliceOp{}, fmt.Errorf("replace_section_content: %w", err)
	}
	bodyStart := findSectionBodyStart(src, sec.ByteStart)
	return agentmd.SpliceOp{
		ByteStart:   bodyStart,
		ByteEnd:     sec.ByteEnd,
		Replacement: op.Content,
	}, nil
}

// findSectionBodyStart returns the byte offset where the section's body
// begins — i.e., the first byte after:
//   - the heading line, and
//   - any immediately-following <!-- @id: ... --> anchor line.
//
// Mirrors the strict positional rule in markdown.findAnchorID: at most
// one blank line of slack between the heading and the anchor.
func findSectionBodyStart(src []byte, sectionStart int) int {
	i := sectionStart
	// Advance past the heading line.
	for i < len(src) && src[i] != '\n' {
		i++
	}
	if i < len(src) {
		i++
	}
	// Optionally allow one blank line.
	bookmark := i
	if i < len(src) && src[i] == '\n' {
		i++
	}
	// If the next line is an @id anchor, consume it too.
	if i < len(src) && bytes.HasPrefix(src[i:], []byte("<!-- @id:")) {
		for i < len(src) && src[i] != '\n' {
			i++
		}
		if i < len(src) {
			i++
		}
	} else {
		// No anchor — undo the blank-line consumption so the body
		// starts where it actually starts.
		i = bookmark
	}
	return i
}
