package memory

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

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

// String renders the DriftPolicy as a stable snake_case identifier used in
// staged target-checksums.json and CLI output. Keep these values stable —
// the M5 apply path matches against them.
func (p DriftPolicy) String() string {
	switch p {
	case RequireSectionContentMatch:
		return "require_section_content_match"
	case RequireSectionResolvable:
		return "require_section_resolvable"
	case RequireFileAbsent:
		return "require_file_absent"
	case RequireFilePresent:
		return "require_file_present"
	default:
		return fmt.Sprintf("drift_policy(%d)", int(p))
	}
}

// MarshalJSON renders DriftPolicy as its String() form, not as the integer
// iota value. Staged targets must be human-readable on disk.
func (p DriftPolicy) MarshalJSON() ([]byte, error) {
	return []byte(`"` + p.String() + `"`), nil
}

// UnmarshalJSON reverses MarshalJSON for the M5 apply path, which round-
// trips target-checksums.json off disk. An unknown identifier is an error
// — silently mapping it to the zero value would mask staging-file
// corruption.
func (p *DriftPolicy) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	switch s {
	case "require_section_content_match":
		*p = RequireSectionContentMatch
	case "require_section_resolvable":
		*p = RequireSectionResolvable
	case "require_file_absent":
		*p = RequireFileAbsent
	case "require_file_present":
		*p = RequireFilePresent
	default:
		return fmt.Errorf("DriftPolicy: unknown identifier %q", s)
	}
	return nil
}

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

// ExtraFile is an additional file an operation produces beyond its
// primary Path() target. archive_section / remove_section use this to
// copy the archived section content into a brand-new archive/ file.
type ExtraFile struct {
	// Path is the forward-slash, memory-relative destination.
	Path string
	// Content is the full bytes to write. The orchestrator treats the
	// file as new (RequireFileAbsent) and write-once.
	Content []byte
}

// ExtraFileProducer is the optional interface an Operation implements
// when it writes to files beyond its primary Path(). The orchestrator
// type-asserts for it during the per-file planning loop and, if present,
// collects the extra files for validation + staging/apply.
//
// Only archive_section and remove_section implement this today; the five
// original operations don't, so they're untouched.
type ExtraFileProducer interface {
	// ExtraFiles computes the additional files this op creates, derived
	// from src — the primary file's bytes at the moment the op runs,
	// BEFORE its own splice is applied. (archive_section reads the
	// section it's about to replace; remove_section reads the section it's
	// about to delete.)
	ExtraFiles(src []byte) ([]ExtraFile, error)
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

	// M4 archival/rename fields.
	ArchivePath     string `json:"archive_path,omitempty"`     // archive_section, remove_section
	Replacement     string `json:"replacement,omitempty"`      // archive_section: new source-section body
	Reason          string `json:"reason,omitempty"`           // remove_section: why it's gone
	NewHeading      string `json:"new_heading,omitempty"`      // rename_heading
	NewHeadingLevel int    `json:"new_heading_level,omitempty"` // rename_heading; 0 = keep current
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
	case "archive_section":
		return &ArchiveSection{
			FilePath:    in.Path,
			SectionID:   in.SectionID,
			Heading:     in.Heading,
			Level:       in.HeadingLevel,
			Occurrence:  in.Occurrence,
			ArchivePath: in.ArchivePath,
			Replacement: []byte(in.Replacement),
		}, nil
	case "remove_section":
		return &RemoveSection{
			FilePath:    in.Path,
			SectionID:   in.SectionID,
			Heading:     in.Heading,
			Level:       in.HeadingLevel,
			Occurrence:  in.Occurrence,
			ArchivePath: in.ArchivePath,
			Reason:      in.Reason,
		}, nil
	case "rename_heading":
		return &RenameHeading{
			FilePath:        in.Path,
			SectionID:       in.SectionID,
			Heading:         in.Heading,
			Level:           in.HeadingLevel,
			Occurrence:      in.Occurrence,
			NewHeading:      in.NewHeading,
			NewHeadingLevel: in.NewHeadingLevel,
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
// the new section is inserted at the "first child slot" of that parent —
// the position of the parent's first existing child (a section inside the
// parent's byte range with strictly greater HeadingLevel). When the parent
// has no children, the insert falls back to parent.ByteEnd (just before
// the parent's next sibling/ancestor heading or EOF).
//
// Without ParentSectionID the new section is appended at EOF.
//
// Why the "first child slot" semantic: a level-1 parent subsumes ALL
// subsequent sections until the next level-1 heading (or EOF). Inserting
// at parent.ByteEnd would put the new child AFTER every other section in
// the file, which is rarely what the agent intends. Inserting before the
// first existing child keeps the new section visually "at the top of the
// parent's contents" — matching how a human would add a sub-section to
// a chapter.
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
	insertAt := len(src) // default: EOF append

	if op.ParentSectionID != "" {
		sections, err := agentmd.ParseSections(src)
		if err != nil {
			return agentmd.SpliceOp{}, fmt.Errorf("append_section: parse: %w", err)
		}
		parent, ok := agentmd.FindByID(sections, op.ParentSectionID)
		if !ok || parent == nil {
			return agentmd.SpliceOp{}, fmt.Errorf("append_section: parent section not found: %q", op.ParentSectionID)
		}

		// First child slot: the first heading strictly inside the parent's
		// range with a greater HeadingLevel. ParseSections returns sections
		// in document order, so the first match is the earliest child.
		insertAt = parent.ByteEnd
		for _, s := range sections {
			if s.ByteStart > parent.ByteStart && s.ByteStart < parent.ByteEnd && s.HeadingLevel > parent.HeadingLevel {
				insertAt = s.ByteStart
				break
			}
		}
	}

	// Insert as a new section: one blank line before the new heading and a
	// blank line after it before whatever follows (or a single newline at
	// EOF). spliceAppend normalizes the seam so the heading never abuts the
	// previous section's last line.
	return spliceAppend(src, insertAt, op.Content, "\n\n"), nil
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
	// Append to the END of the section's CONTENT — after its last non-blank
	// line, not after its trailing blank line. Inserting at sec.ByteEnd
	// (which sits past the blank line that separates this section from the
	// next heading) would detach the new text from the body and glue it to
	// the following heading. lead="\n" keeps the new text part of this
	// section; spliceAppend restores the blank line before the next heading.
	return spliceAppend(src, sec.ByteEnd, op.Content, "\n"), nil
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

// directBody returns the bytes of the section at sections[idx]'s body
// only — heading and optional @id anchor stripped off, descendant
// (deeper-level) sections excluded.
//
// Used by the orchestrator's "affected sections" check (update.go):
// when an op adds a new child under an existing parent, the parent's
// full content range expands to include the new child, but the parent's
// own authored body didn't change. directBody captures that distinction.
//
// sections must be the slice ParseSections returned over src; the
// document-order invariant lets directBody find the first descendant
// in O(N) without a tree lookup.
func directBody(src []byte, sections []agentmd.Section, idx int) []byte {
	if idx < 0 || idx >= len(sections) {
		return nil
	}
	sec := sections[idx]
	bodyStart := findSectionBodyStart(src, sec.ByteStart)
	bodyEnd := sec.ByteEnd
	// ParseSections returns document order, so sections[idx+1] is the
	// immediately-following section. If it starts inside sec's range it
	// is sec's first descendant — cut the body off right before it.
	// (A parent's first child always directly follows it in document
	// order, before any sibling.) Anything at/after sec.ByteEnd is a
	// sibling/ancestor and leaves the body running to sec.ByteEnd.
	if next := idx + 1; next < len(sections) && sections[next].ByteStart < sec.ByteEnd {
		bodyEnd = sections[next].ByteStart
	}
	if bodyStart > bodyEnd {
		return nil
	}
	return src[bodyStart:bodyEnd]
}

// headingLineEnd returns the byte offset just before the newline that
// terminates the heading line starting at sectionStart, or len(src) if
// the heading is the last line with no trailing newline. The newline
// itself is NOT included in [sectionStart, result).
func headingLineEnd(src []byte, sectionStart int) int {
	i := sectionStart
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

// isArchivePath reports whether rel (forward-slash) is inside archive/.
func isArchivePath(rel string) bool {
	return strings.HasPrefix(rel, "archive/")
}

// spliceAppend builds a whitespace-normalized insertion of `block` at the end
// of the content that precedes `boundary` — the byte offset of the next
// heading, or len(src) at EOF.
//
// The naive "insert at sec.ByteEnd" splices AFTER a section's trailing blank
// line, which detaches the new text from the section body and glues it to the
// following heading. Instead this finds the last non-whitespace byte before
// `boundary`, drops the existing trailing whitespace there, and re-emits a
// clean seam: `lead` between the prior content and the block, then one blank
// line before the following heading (or a single newline at EOF). The block's
// own trailing newlines are trimmed first so separation is exact. Only the
// few bytes at the seam are rewritten; everything else is byte-preserved.
//
// lead is "\n" for append_to_section (the block continues the section's
// content) and "\n\n" for append_section (a new section needs a blank line
// before its heading).
func spliceAppend(src []byte, boundary int, block []byte, lead string) agentmd.SpliceOp {
	contentEnd := boundary
	for contentEnd > 0 && isHspaceOrNewline(src[contentEnd-1]) {
		contentEnd--
	}
	repl := bytes.TrimRight(block, "\r\n")
	if contentEnd > 0 {
		repl = append([]byte(lead), repl...)
	}
	if boundary < len(src) {
		repl = append(repl, '\n', '\n') // blank line before the following heading
	} else {
		repl = append(repl, '\n') // EOF: single terminating newline
	}
	return agentmd.SpliceOp{ByteStart: contentEnd, ByteEnd: boundary, Replacement: repl}
}

// isHspaceOrNewline reports whether b is ASCII horizontal whitespace or a
// line break — the bytes spliceAppend trims when locating content end.
func isHspaceOrNewline(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// ============================================================================
// ArchiveSection (M4)
// ============================================================================

// ArchiveSection copies a section's current content to a new archive
// file and replaces the source section (heading included) with a
// pointer/stub Replacement. The archive file is write-once: it must not
// already exist. Per design §15.8, archiving never destroys content and
// always stages.
//
// This is a MULTI-FILE operation: Plan() handles the source-file splice,
// and ExtraFiles() produces the new archive file. The orchestrator wires
// both together.
type ArchiveSection struct {
	FilePath    string
	SectionID   string
	Heading     string
	Level       int
	Occurrence  int
	ArchivePath string
	Replacement []byte // new content for the source section (heading + anchor + stub)
}

func (op *ArchiveSection) Kind() string { return "archive_section" }
func (op *ArchiveSection) Path() string { return op.FilePath }

func (op *ArchiveSection) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("archive_section: path is required")
	}
	if op.SectionID == "" && op.Heading == "" {
		return errors.New("archive_section: section_id or heading is required")
	}
	if op.ArchivePath == "" {
		return errors.New("archive_section: archive_path is required")
	}
	if !isArchivePath(op.ArchivePath) {
		return fmt.Errorf("archive_section: archive_path %q must be inside archive/", op.ArchivePath)
	}
	if len(op.Replacement) == 0 {
		return errors.New("archive_section: replacement is required (the stub left in place of the archived section)")
	}
	if err := agentmd.ValidateMarkdown(op.Replacement); err != nil {
		return fmt.Errorf("archive_section: replacement does not parse as Markdown: %w", err)
	}
	return nil
}

func (op *ArchiveSection) Targets() []OperationTarget {
	return []OperationTarget{
		{Path: op.FilePath, SectionID: op.SectionID, Policy: RequireSectionContentMatch},
		{Path: op.ArchivePath, Policy: RequireFileAbsent},
	}
}

func (op *ArchiveSection) Plan(src []byte) (agentmd.SpliceOp, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return agentmd.SpliceOp{}, fmt.Errorf("archive_section: %w", err)
	}
	return agentmd.SpliceOp{
		ByteStart:   sec.ByteStart,
		ByteEnd:     sec.ByteEnd,
		Replacement: op.Replacement,
	}, nil
}

// ExtraFiles copies the section's current bytes (heading included) into
// the archive file. src is the source file's bytes BEFORE this op's
// splice, so the section is still present.
func (op *ArchiveSection) ExtraFiles(src []byte) ([]ExtraFile, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return nil, fmt.Errorf("archive_section: %w", err)
	}
	content := make([]byte, sec.ByteEnd-sec.ByteStart)
	copy(content, src[sec.ByteStart:sec.ByteEnd])
	return []ExtraFile{{Path: op.ArchivePath, Content: content}}, nil
}

// ============================================================================
// RemoveSection (M4)
// ============================================================================

// RemoveSection archives a section to a new write-once archive file,
// then splices the section out of the source entirely (heading
// included). Per design §15.9, removal is archive-first — content is
// preserved in archive/ before the source loses it — and always stages.
type RemoveSection struct {
	FilePath    string
	SectionID   string
	Heading     string
	Level       int
	Occurrence  int
	ArchivePath string
	Reason      string // why it's being removed; recorded as a comment in the archive
}

func (op *RemoveSection) Kind() string { return "remove_section" }
func (op *RemoveSection) Path() string { return op.FilePath }

func (op *RemoveSection) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("remove_section: path is required")
	}
	if op.SectionID == "" && op.Heading == "" {
		return errors.New("remove_section: section_id or heading is required")
	}
	if op.ArchivePath == "" {
		return errors.New("remove_section: archive_path is required (removal is archive-first)")
	}
	if !isArchivePath(op.ArchivePath) {
		return fmt.Errorf("remove_section: archive_path %q must be inside archive/", op.ArchivePath)
	}
	return nil
}

func (op *RemoveSection) Targets() []OperationTarget {
	return []OperationTarget{
		{Path: op.FilePath, SectionID: op.SectionID, Policy: RequireSectionContentMatch},
		{Path: op.ArchivePath, Policy: RequireFileAbsent},
	}
}

func (op *RemoveSection) Plan(src []byte) (agentmd.SpliceOp, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return agentmd.SpliceOp{}, fmt.Errorf("remove_section: %w", err)
	}
	// Splice the whole section (heading through just-before-next-heading)
	// out entirely. The empty replacement deletes it.
	return agentmd.SpliceOp{
		ByteStart:   sec.ByteStart,
		ByteEnd:     sec.ByteEnd,
		Replacement: nil,
	}, nil
}

// ExtraFiles archives the section content. When Reason is set, it's
// prepended as an HTML comment so the archive file records WHY the
// section was removed without affecting rendered output.
func (op *RemoveSection) ExtraFiles(src []byte) ([]ExtraFile, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return nil, fmt.Errorf("remove_section: %w", err)
	}
	body := src[sec.ByteStart:sec.ByteEnd]
	var content []byte
	if op.Reason != "" {
		header := fmt.Sprintf("<!-- removed from %s: %s -->\n\n", op.FilePath, op.Reason)
		content = make([]byte, 0, len(header)+len(body))
		content = append(content, header...)
		content = append(content, body...)
	} else {
		content = make([]byte, len(body))
		copy(content, body)
	}
	return []ExtraFile{{Path: op.ArchivePath, Content: content}}, nil
}

// ============================================================================
// RenameHeading (M4)
// ============================================================================

// RenameHeading changes a section's heading text (and optionally its
// level, constrained to ±1) while preserving the @id anchor and all
// bytes outside the heading line. Per design §15.10.
type RenameHeading struct {
	FilePath        string
	SectionID       string
	Heading         string
	Level           int
	Occurrence      int
	NewHeading      string
	NewHeadingLevel int // 0 = keep current level
}

func (op *RenameHeading) Kind() string { return "rename_heading" }
func (op *RenameHeading) Path() string { return op.FilePath }

func (op *RenameHeading) Validate(sch *schema.Schema) error {
	if op.FilePath == "" {
		return errors.New("rename_heading: path is required")
	}
	if op.SectionID == "" && op.Heading == "" {
		return errors.New("rename_heading: section_id or heading is required")
	}
	if op.NewHeading == "" {
		return errors.New("rename_heading: new_heading is required")
	}
	if op.NewHeadingLevel != 0 && (op.NewHeadingLevel < 1 || op.NewHeadingLevel > 6) {
		return fmt.Errorf("rename_heading: new_heading_level must be 1-6 (or 0 to keep current), got %d", op.NewHeadingLevel)
	}
	return nil
}

func (op *RenameHeading) Targets() []OperationTarget {
	return []OperationTarget{{
		Path:      op.FilePath,
		SectionID: op.SectionID,
		// Resolvable (not content-match): rename only touches the heading
		// line, found by ID; the body can have grown since staging.
		Policy: RequireSectionResolvable,
	}}
}

func (op *RenameHeading) Plan(src []byte) (agentmd.SpliceOp, error) {
	sec, err := resolveSection(src, op.SectionID, op.Heading, op.Level, op.Occurrence)
	if err != nil {
		return agentmd.SpliceOp{}, fmt.Errorf("rename_heading: %w", err)
	}
	level := op.NewHeadingLevel
	if level == 0 {
		level = sec.HeadingLevel
	}
	// Constrain level change to ±1 of the current to avoid restructuring.
	delta := level - sec.HeadingLevel
	if delta < -1 || delta > 1 {
		return agentmd.SpliceOp{}, fmt.Errorf(
			"rename_heading: level change %d→%d exceeds ±1 (would restructure the document)",
			sec.HeadingLevel, level)
	}
	lineEnd := headingLineEnd(src, sec.ByteStart)
	newLine := strings.Repeat("#", level) + " " + op.NewHeading
	return agentmd.SpliceOp{
		ByteStart:   sec.ByteStart,
		ByteEnd:     lineEnd,
		Replacement: []byte(newLine),
	}, nil
}
