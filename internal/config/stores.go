package config

import (
	"fmt"
	"regexp"
)

// Federation: a project may reference one or more shared "landscape" stores
// (design doc docs/design/federated-memory.md §6.1). This file defines the
// manifest `stores` block and its validation. It is config/contract only —
// sync (PR3), the index store dimension (PR4), and multi-store fetch (PR5)
// build on it. With no stores declared, behavior is unchanged.

// DefaultStorePath is the store directory within a referenced repo.
const DefaultStorePath = ".agent-memory"

// DefaultStorePriority is the ranking multiplier applied to a referenced
// store's results (the local store is 1.0). It is applied as a multiplier on
// the existing negative-BM25-derived score (see internal/index/ranking.go), so
// a value below 1 PENALIZES a store relative to local — the intended default
// for landscape memory. Do not "fix the sign".
const DefaultStorePriority = 0.8

// StoreModeReadOnly is the only supported mode in slice 1: a consuming repo
// reads a referenced store but never writes to it. read-write (cross-repo
// propose) is a later design.
const StoreModeReadOnly = "read-only"

// storeNameRe constrains a store name to a filesystem-safe slug: the name
// becomes a cache directory and a provenance label.
var storeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Store is one referenced landscape store.
type Store struct {
	Name               string  `yaml:"name"`
	Source             string  `yaml:"source"`                        // git URL or local path
	Revision           string  `yaml:"revision,omitempty"`            // branch/tag/commit; default branch if empty
	Path               string  `yaml:"path,omitempty"`                // store dir within the repo; default ".agent-memory"
	Mode               string  `yaml:"mode,omitempty"`                // "read-only" (default/only in slice 1)
	PriorityMultiplier float64 `yaml:"priority_multiplier,omitempty"` // ranking multiplier; default 0.8
}

// StorePath returns the in-repo store directory, defaulting to ".agent-memory".
func (s Store) StorePath() string {
	if s.Path == "" {
		return DefaultStorePath
	}
	return s.Path
}

// Priority returns the ranking multiplier, defaulting to DefaultStorePriority.
func (s Store) Priority() float64 {
	if s.PriorityMultiplier == 0 {
		return DefaultStorePriority
	}
	return s.PriorityMultiplier
}

// EffectiveMode returns the store mode, defaulting to read-only.
func (s Store) EffectiveMode() string {
	if s.Mode == "" {
		return StoreModeReadOnly
	}
	return s.Mode
}

// validateStores checks the manifest's stores block: unique safe-slug names,
// a non-empty source, a recognised mode, and a non-negative priority.
func validateStores(stores []Store) error {
	seen := make(map[string]struct{}, len(stores))
	for i, s := range stores {
		if !storeNameRe.MatchString(s.Name) {
			return fmt.Errorf("manifest: stores[%d]: name %q must match %s", i, s.Name, storeNameRe.String())
		}
		if _, dup := seen[s.Name]; dup {
			return fmt.Errorf("manifest: stores[%d]: duplicate store name %q", i, s.Name)
		}
		seen[s.Name] = struct{}{}
		if s.Source == "" {
			return fmt.Errorf("manifest: stores[%d] (%s): source is required", i, s.Name)
		}
		if s.Mode != "" && s.Mode != StoreModeReadOnly {
			return fmt.Errorf("manifest: stores[%d] (%s): mode %q unsupported (only %q)", i, s.Name, s.Mode, StoreModeReadOnly)
		}
		if s.PriorityMultiplier < 0 {
			return fmt.Errorf("manifest: stores[%d] (%s): priority_multiplier must be >= 0", i, s.Name)
		}
	}
	return nil
}
