// Package schema loads and validates .agent-memory/meta/schema.yaml. The
// schema declares the categories of files the server knows about (durable
// decisions, modules, archive, local current/sessions, server-managed
// index) and the per-category policy that drives M3 approval routing and
// validation.
//
// The manifest (internal/config) imports this package for ApprovalMode and
// references categories by name in its updates.approval block.
//
// See docs/patterns/configuration-loading.md and design doc v0.4.1 §25.
package schema

import (
	"errors"
	"fmt"
	"os"
	"path"

	"gopkg.in/yaml.v3"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

// ApprovalMode is the approval policy assigned to a category or
// (in the manifest) overridden per-operation.
type ApprovalMode string

const (
	// ApprovalApply means writes are applied immediately after validation.
	ApprovalApply ApprovalMode = "apply"
	// ApprovalStage means writes are staged for human review via the
	// review/apply CLI commands.
	ApprovalStage ApprovalMode = "stage"
	// ApprovalServerOnly means agents may not write to this category at all;
	// the server maintains it (e.g., index.md).
	ApprovalServerOnly ApprovalMode = "server_only"
)

// IsValid reports whether m is one of the recognised ApprovalMode values.
func (m ApprovalMode) IsValid() bool {
	switch m {
	case ApprovalApply, ApprovalStage, ApprovalServerOnly:
		return true
	}
	return false
}

// Schema is the top-level deserialisation target for schema.yaml.
type Schema struct {
	Version    string              `yaml:"version"`
	Categories map[string]Category `yaml:"categories"`
}

// Category declares the rules for a class of memory files.
type Category struct {
	// Name is populated from the map key by populateCategoryNames() after
	// load. It is NOT read from YAML directly.
	Name string `yaml:"-"`

	// Exactly one of File or FileGlob identifies the category's files.
	File     string `yaml:"file,omitempty"`
	FileGlob string `yaml:"file_glob,omitempty"`

	// SectionIDRequired turns on the AssignMissingIDs pass during init and
	// rebuild-index for files in this category.
	SectionIDRequired bool `yaml:"section_id_required,omitempty"`

	// SectionSchema is the per-section structural schema (required fields,
	// patterns, enums). M1 stores it verbatim; M3 will validate against it.
	SectionSchema *SectionSchema `yaml:"section_schema,omitempty"`

	// Approval is the default approval policy for writes to this category.
	// The manifest may override per-operation in updates.approval.
	Approval ApprovalMode `yaml:"approval,omitempty"`

	// ServerManaged means the file is written only by the server (e.g.,
	// index.md). AgentWritable must be false in this case.
	ServerManaged bool `yaml:"server_managed,omitempty"`
	AgentWritable bool `yaml:"agent_writable,omitempty"`

	// WriteOnce means files in this category may not be modified after
	// creation (e.g., archive/*.md).
	WriteOnce bool `yaml:"write_once,omitempty"`

	// GitTracked indicates whether agent-memory init's .gitignore should
	// keep this file out of git. false here corresponds to entries in
	// .agent-memory/.gitignore (current/, sessions/, etc.).
	GitTracked bool `yaml:"git_tracked"`

	Provenance Provenance `yaml:"provenance,omitempty"`
}

// SectionSchema describes the required structure of an individual section
// within a category file. The validator that consumes these fields lands
// in M3; M1 only stores and round-trips them.
type SectionSchema struct {
	RequiredTopLevelHeading  bool        `yaml:"required_top_level_heading,omitempty"`
	PerSectionRequiredFields []FieldSpec `yaml:"per_section_required_fields,omitempty"`
	PerSectionOptionalFields []FieldSpec `yaml:"per_section_optional_fields,omitempty"`
}

// FieldSpec describes one labelled field inside a section's body
// (e.g., "Date: 2026-05-26").
type FieldSpec struct {
	Name    string   `yaml:"name"`
	Pattern string   `yaml:"pattern,omitempty"`
	Enum    []string `yaml:"enum,omitempty"`
}

// Provenance is the source-attribution policy for a category.
type Provenance struct {
	Required               bool     `yaml:"required,omitempty"`
	RequiredForNewSections bool     `yaml:"required_for_new_sections,omitempty"`
	AllowedSourceTypes     []string `yaml:"allowed_source_types,omitempty"`
	ForbiddenSourceTypes   []string `yaml:"forbidden_source_types,omitempty"`
}

// DefaultSchema returns the recommended schema from design doc v0.4.1 §25.1.
// The returned Schema has Category.Name populated for every entry — callers
// who consume *Schema directly (without going through LoadSchema) get a
// self-consistent struct.
func DefaultSchema() *Schema {
	s := defaultSchema()
	s.populateCategoryNames()
	return s
}

func defaultSchema() *Schema {
	return &Schema{
		Version: "0.4.1",
		Categories: map[string]Category{
			"index": {
				File:          "index.md",
				ServerManaged: true,
				AgentWritable: false,
				GitTracked:    true,
				Approval:      ApprovalServerOnly,
			},
			"conventions": {
				File:              "conventions.md",
				SectionIDRequired: true,
				Approval:          ApprovalStage,
				AgentWritable:     true,
				GitTracked:        true,
				Provenance: Provenance{
					RequiredForNewSections: true,
					AllowedSourceTypes:     []string{"file", "test", "user"},
				},
			},
			"decisions": {
				File:              "decisions.md",
				SectionIDRequired: true,
				Approval:          ApprovalStage,
				AgentWritable:     true,
				GitTracked:        true,
				Provenance: Provenance{
					Required:             true,
					AllowedSourceTypes:   []string{"file", "test", "user"},
					ForbiddenSourceTypes: []string{"external", "inference"},
				},
				// Decisions carry structured metadata. The orchestrator
				// validates only sections this proposal created or
				// modified, so legacy sections written before this
				// schema landed stay untouched until the user edits
				// them.
				//
				// Required fields per section:
				//   Date       — ISO 8601 date (when the decision was made)
				//   Status     — active | superseded | deprecated | proposed
				//   Confidence — confirmed | inferred | user-provided
				//
				// The parser accepts plain (`Date: ...`), bold
				// (`**Date:** ...`), and italic (`*Date:* ...`) field
				// shapes interchangeably.
				SectionSchema: &SectionSchema{
					PerSectionRequiredFields: []FieldSpec{
						{Name: "Date", Pattern: `^\d{4}-\d{2}-\d{2}$`},
						{Name: "Status", Enum: []string{"active", "superseded", "deprecated", "proposed"}},
						{Name: "Confidence", Enum: []string{"confirmed", "inferred", "user-provided"}},
					},
				},
			},
			"pitfalls": {
				File:              "pitfalls.md",
				SectionIDRequired: true,
				// Append-style updates apply; section-level replace requires
				// staging (enforced by the per-operation policy in the
				// manifest's updates.approval block).
				Approval:      ApprovalApply,
				AgentWritable: true,
				GitTracked:    true,
				Provenance: Provenance{
					RequiredForNewSections: true,
				},
			},
			"modules": {
				FileGlob:          "modules/*.md",
				SectionIDRequired: true,
				Approval:          ApprovalStage,
				AgentWritable:     true,
				GitTracked:        true,
				Provenance: Provenance{
					Required: true,
				},
			},
			"archive": {
				FileGlob:      "archive/*.md",
				WriteOnce:     true,
				Approval:      ApprovalStage,
				AgentWritable: true,
				GitTracked:    true,
			},
			"current": {
				FileGlob:      "local/current.*.md",
				Approval:      ApprovalApply,
				AgentWritable: true,
				GitTracked:    false,
			},
			"sessions": {
				FileGlob:      "sessions/*.md",
				Approval:      ApprovalApply,
				AgentWritable: true,
				GitTracked:    false,
			},
			// Federation landscape kinds (federated-memory design §6.3).
			// Declared in every store's default schema but authored only in a
			// landscape / platform-memory store; a normal repo never creates
			// these files, so the categories are inert there.
			"component": {
				File:              "components.md",
				SectionIDRequired: true,
				Approval:          ApprovalStage,
				AgentWritable:     true,
				GitTracked:        true,
				SectionSchema: &SectionSchema{
					PerSectionRequiredFields: []FieldSpec{{Name: "Owner"}},
					PerSectionOptionalFields: []FieldSpec{{Name: "Repo"}, {Name: "Summary"}},
				},
			},
			"contract": {
				File:              "contracts.md",
				SectionIDRequired: true,
				Approval:          ApprovalStage,
				AgentWritable:     true,
				GitTracked:        true,
				SectionSchema: &SectionSchema{
					PerSectionRequiredFields: []FieldSpec{
						{Name: "Kind", Enum: []string{"http", "event"}},
						{Name: "Direction", Enum: []string{"produces", "consumes"}},
					},
					PerSectionOptionalFields: []FieldSpec{{Name: "Owner"}, {Name: "Summary"}},
				},
			},
			"actor": {
				File:              "actors.md",
				SectionIDRequired: true,
				Approval:          ApprovalStage,
				AgentWritable:     true,
				GitTracked:        true,
				SectionSchema: &SectionSchema{
					PerSectionOptionalFields: []FieldSpec{{Name: "Contact"}},
				},
			},
		},
	}
}

// LoadSchema reads schema.yaml from schemaPath. The YAML is decoded into a
// fresh Schema, then merged INTO DefaultSchema field-by-field so partial
// overrides work intuitively:
//
//   - YAML mentioning one field in one category overrides only that field;
//     the rest of the category and all other categories keep their defaults.
//   - User-defined categories not in defaults are accepted verbatim.
//
// Merge semantics for individual Category fields:
//
//   - String / pointer / slice fields: empty/nil/[] in YAML means
//     "not set"; the default is preserved.
//   - Bool fields: true overrides; false is treated as "not set" because
//     Go's zero value for bool is indistinguishable from an explicit
//     `field: false` in YAML. To flip a default-true bool to false, write
//     the full category structure (documented limitation).
//
// Naive yaml.v3 merge does NOT work here because it preserves only map
// KEYS (categories not mentioned in YAML survive), not field-level merge
// INTO existing map VALUES. The custom merge below handles both layers.
func LoadSchema(schemaPath string) (*Schema, error) {
	b, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("LoadSchema: %w", err)
	}
	var loaded Schema
	if err := yaml.Unmarshal(b, &loaded); err != nil {
		return nil, fmt.Errorf("LoadSchema: parse %q: %w", schemaPath, err)
	}

	s := DefaultSchema()
	if loaded.Version != "" {
		s.Version = loaded.Version
	}
	for name, lcat := range loaded.Categories {
		if dcat, ok := s.Categories[name]; ok {
			s.Categories[name] = mergeCategory(dcat, lcat)
		} else {
			// User-defined category not in defaults — accept verbatim.
			s.Categories[name] = lcat
		}
	}
	s.populateCategoryNames()
	return s, nil
}

// WriteSchema serialises s to schemaPath atomically.
func WriteSchema(schemaPath string, s *Schema) error {
	b, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("WriteSchema: marshal: %w", err)
	}
	return agentfs.WriteAtomic(schemaPath, b, 0644)
}

// WriteDefault writes the recommended schema to schemaPath. Used by
// `agent-memory init` (T1.10).
func WriteDefault(schemaPath string) error {
	return WriteSchema(schemaPath, DefaultSchema())
}

// mergeCategory returns defaults with non-zero fields from loaded applied
// as overrides. See LoadSchema for the merge semantics. Exported for
// test visibility within the package; not part of the public API.
func mergeCategory(defaults, loaded Category) Category {
	if loaded.File != "" {
		defaults.File = loaded.File
	}
	if loaded.FileGlob != "" {
		defaults.FileGlob = loaded.FileGlob
	}
	if loaded.Approval != "" {
		defaults.Approval = loaded.Approval
	}
	if loaded.SectionIDRequired {
		defaults.SectionIDRequired = true
	}
	if loaded.ServerManaged {
		defaults.ServerManaged = true
	}
	if loaded.AgentWritable {
		defaults.AgentWritable = true
	}
	if loaded.WriteOnce {
		defaults.WriteOnce = true
	}
	if loaded.GitTracked {
		defaults.GitTracked = true
	}
	if loaded.SectionSchema != nil {
		defaults.SectionSchema = loaded.SectionSchema
	}
	defaults.Provenance = mergeProvenance(defaults.Provenance, loaded.Provenance)
	return defaults
}

func mergeProvenance(defaults, loaded Provenance) Provenance {
	if loaded.Required {
		defaults.Required = true
	}
	if loaded.RequiredForNewSections {
		defaults.RequiredForNewSections = true
	}
	if len(loaded.AllowedSourceTypes) > 0 {
		defaults.AllowedSourceTypes = loaded.AllowedSourceTypes
	}
	if len(loaded.ForbiddenSourceTypes) > 0 {
		defaults.ForbiddenSourceTypes = loaded.ForbiddenSourceTypes
	}
	return defaults
}

// CategoryForPath returns the category whose File or FileGlob matches rel.
// rel must use forward slashes (the canonical form used in design and
// manifest examples).
//
// Lookup order:
//  1. Exact File match.
//  2. FileGlob via path.Match — uses '/' as the separator on every OS, so
//     '*' in the glob never spans directory boundaries. (path/filepath
//     would use '\' on Windows, letting '*' eat slashes, which is wrong
//     for our convention.)
//
// The returned Category has Name populated from the map key, even if the
// Schema didn't go through populateCategoryNames() upstream. Callers can
// rely on Name being non-empty whenever ok == true.
//
// Returns (Category{}, false) if no category matches.
func (s *Schema) CategoryForPath(rel string) (Category, bool) {
	// Exact match first.
	for name, cat := range s.Categories {
		if cat.File != "" && cat.File == rel {
			cat.Name = name
			return cat, true
		}
	}
	// Glob match (forward-slash semantics, OS-independent).
	for name, cat := range s.Categories {
		if cat.FileGlob == "" {
			continue
		}
		if ok, err := path.Match(cat.FileGlob, rel); ok && err == nil {
			cat.Name = name
			return cat, true
		}
	}
	return Category{}, false
}

// Validate checks basic invariants on s:
//   - Version is non-empty.
//   - Every category has either File or FileGlob (never both empty).
//   - Every Approval value (if non-empty) is a recognised ApprovalMode.
//   - ServerManaged categories are not AgentWritable.
func (s *Schema) Validate() error {
	if s.Version == "" {
		return errors.New("schema: version is required")
	}
	for name, cat := range s.Categories {
		if cat.File == "" && cat.FileGlob == "" {
			return fmt.Errorf("schema: category %q has neither file nor file_glob", name)
		}
		if cat.File != "" && cat.FileGlob != "" {
			return fmt.Errorf("schema: category %q specifies both file and file_glob", name)
		}
		if cat.Approval != "" && !cat.Approval.IsValid() {
			return fmt.Errorf("schema: category %q: invalid approval mode %q", name, cat.Approval)
		}
		if cat.ServerManaged && cat.AgentWritable {
			return fmt.Errorf("schema: category %q is server_managed and agent_writable (mutually exclusive)", name)
		}
	}
	return nil
}

// populateCategoryNames copies map keys into Category.Name so callers of
// CategoryForPath don't have to track the lookup key separately.
func (s *Schema) populateCategoryNames() {
	for name, cat := range s.Categories {
		cat.Name = name
		s.Categories[name] = cat
	}
}
