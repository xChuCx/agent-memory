package schema

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// SectionViolation is one rule-broken finding from ValidateSection.
type SectionViolation struct {
	Field   string // the FieldSpec.Name; empty for category-level violations
	Message string // human-readable
}

func (v SectionViolation) String() string {
	if v.Field == "" {
		return v.Message
	}
	return fmt.Sprintf("%s: %s", v.Field, v.Message)
}

// ValidateSection checks one section's body against the category's
// SectionSchema (required fields, patterns, enums). Returns a slice of
// violations; empty slice means the body conforms (or the category
// declares no SectionSchema, in which case anything goes).
//
// Field parsing is intentionally simple: single-line `Name: Value` pairs.
// Multi-line values, indented continuations, and structured sub-fields
// are not supported in M3. The validator runs on the section body bytes
// (everything after the heading line and optional @id anchor) — the
// caller is responsible for slicing that out.
//
// Examples of what gets caught for the decisions category:
//
//	## Use Postgres
//	<!-- @id: use-postgres -->
//
//	Status: active            ← matched against Status enum
//	Confidence: confirmed     ← matched against Confidence enum
//	(no Date field)           ← reported as missing required field
func ValidateSection(cat Category, sectionBody []byte) []SectionViolation {
	if cat.SectionSchema == nil {
		return nil
	}
	fields := parseFieldLines(sectionBody)
	var violations []SectionViolation

	for _, fs := range cat.SectionSchema.PerSectionRequiredFields {
		v, present := fields[fs.Name]
		if !present {
			violations = append(violations, SectionViolation{
				Field:   fs.Name,
				Message: "required field missing",
			})
			continue
		}
		violations = append(violations, validateFieldValue(fs, v)...)
	}
	for _, fs := range cat.SectionSchema.PerSectionOptionalFields {
		v, present := fields[fs.Name]
		if !present {
			continue
		}
		violations = append(violations, validateFieldValue(fs, v)...)
	}
	return violations
}

// validateFieldValue checks one resolved value against a FieldSpec's
// Pattern and Enum (both optional).
func validateFieldValue(fs FieldSpec, value string) []SectionViolation {
	var out []SectionViolation
	if fs.Pattern != "" {
		re, err := regexp.Compile(fs.Pattern)
		if err != nil {
			out = append(out, SectionViolation{
				Field:   fs.Name,
				Message: fmt.Sprintf("schema pattern %q is invalid regex: %v", fs.Pattern, err),
			})
		} else if !re.MatchString(value) {
			out = append(out, SectionViolation{
				Field:   fs.Name,
				Message: fmt.Sprintf("value %q does not match pattern %q", value, fs.Pattern),
			})
		}
	}
	if len(fs.Enum) > 0 {
		ok := false
		for _, e := range fs.Enum {
			if value == e {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, SectionViolation{
				Field:   fs.Name,
				Message: fmt.Sprintf("value %q is not in allowed values %v", value, fs.Enum),
			})
		}
	}
	return out
}

// parseFieldLines extracts `Name: Value` style fields from the section body.
//
// A line counts as a field line iff:
//   - it contains a colon
//   - the part before the colon is a plausible field name (letters, spaces,
//     dashes only)
//   - the field name is at the start of the line (no leading indent)
//
// Duplicate field names: the first occurrence wins. This is consistent
// with the ADR-like format from design doc §10.4 where each field appears
// once.
func parseFieldLines(body []byte) map[string]string {
	fields := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := sc.Text()
		// Skip lines that start with whitespace (continuation, list item, etc.).
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '-' || line[0] == '*' {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		if !looksLikeFieldName(name) {
			continue
		}
		value := strings.TrimSpace(line[idx+1:])
		if _, exists := fields[name]; !exists {
			fields[name] = value
		}
	}
	return fields
}

// looksLikeFieldName returns true for ASCII letter / digit / space / dash
// strings. Used to reject false positives like "https://example.com" which
// has a colon but isn't a field declaration.
func looksLikeFieldName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == ' ' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}
