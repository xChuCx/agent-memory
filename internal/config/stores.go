package config

import (
	"fmt"
	"path"
	"regexp"
	"strings"
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

// reservedLocalStoreName is the store name the index uses for the consuming
// repo's own content (kept in sync with index.LocalStore — config must not
// import index). A referenced store may not claim it, or its rows would
// collide with the local store in the shadow index.
const reservedLocalStoreName = "local"

// Store is one referenced landscape store.
type Store struct {
	Name               string   `yaml:"name"`
	Source             string   `yaml:"source"`                        // git URL or local path
	Revision           string   `yaml:"revision,omitempty"`            // branch/tag/commit; default branch if empty
	Path               string   `yaml:"path,omitempty"`                // store dir within the repo; default ".agent-memory"
	Mode               string   `yaml:"mode,omitempty"`                // "read-only" (default/only in slice 1)
	PriorityMultiplier *float64 `yaml:"priority_multiplier,omitempty"` // omitted = default (0.8); must be > 0 if set
}

// StorePath returns the in-repo store directory, defaulting to ".agent-memory".
func (s Store) StorePath() string {
	if s.Path == "" {
		return DefaultStorePath
	}
	return s.Path
}

// Priority returns the ranking multiplier, defaulting to DefaultStorePriority
// when unset. A set value is guaranteed > 0 by validateStores, so there is no
// ambiguity between "unset" and an explicit zero.
func (s Store) Priority() float64 {
	if s.PriorityMultiplier == nil {
		return DefaultStorePriority
	}
	return *s.PriorityMultiplier
}

// EffectiveMode returns the store mode, defaulting to read-only.
func (s Store) EffectiveMode() string {
	if s.Mode == "" {
		return StoreModeReadOnly
	}
	return s.Mode
}

// validateStores checks the manifest's stores block: unique safe-slug names,
// a non-empty source, a recognised mode, a positive priority (when set), and a
// safe relative store path.
func validateStores(stores []Store) error {
	seen := make(map[string]struct{}, len(stores))
	for i, s := range stores {
		if !storeNameRe.MatchString(s.Name) {
			return fmt.Errorf("manifest: stores[%d]: name %q must match %s", i, s.Name, storeNameRe.String())
		}
		if s.Name == reservedLocalStoreName {
			return fmt.Errorf("manifest: stores[%d]: name %q is reserved (it labels the consuming repo's own store in the index)", i, s.Name)
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
		if s.PriorityMultiplier != nil && *s.PriorityMultiplier <= 0 {
			return fmt.Errorf("manifest: stores[%d] (%s): priority_multiplier must be > 0 when set (omit it for the default %g)", i, s.Name, DefaultStorePriority)
		}
		if err := validateStorePath(s.Path); err != nil {
			return fmt.Errorf("manifest: stores[%d] (%s): %w", i, s.Name, err)
		}
	}
	return nil
}

// validateStorePath checks a store's in-repo path declaratively. The actual
// filesystem sandboxing (symlink rejection, containment of the synced tree)
// lands with sync in PR3; here we reject paths that could never be a safe,
// portable, in-repo subdirectory. Empty is allowed and defaults to
// ".agent-memory" (see Store.StorePath). The path uses forward slashes (the
// project-wide convention) and must be a clean, relative subdirectory.
func validateStorePath(p string) error {
	if p == "" {
		return nil
	}
	if strings.ContainsRune(p, '\\') {
		return fmt.Errorf("path %q must use forward slashes", p)
	}
	if len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) {
		return fmt.Errorf("path %q must be relative (no drive letter)", p)
	}
	if path.IsAbs(p) {
		return fmt.Errorf("path %q must be relative", p)
	}
	if c := path.Clean(p); c != p {
		return fmt.Errorf("path %q must be in clean form (e.g. %q)", p, c)
	}
	if p == "." || p == ".." {
		return fmt.Errorf("path %q must name a subdirectory", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("path %q must not contain '..'", p)
		}
	}
	return nil
}
