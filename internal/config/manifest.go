// Package config loads and validates .agent-memory/meta/manifest.yaml.
// The manifest holds operational settings (budgets, staging TTL, security
// flags, git policy, per-category approval overrides) — distinct from the
// per-category structural rules in internal/schema.
//
// See docs/patterns/configuration-loading.md and design doc v0.4.1 §26.
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
	"github.com/xChuCx/agent-memory/internal/schema"
)

// Manifest is the deserialisation target for manifest.yaml.
type Manifest struct {
	Version            string      `yaml:"version"`
	StoreFormatVersion int         `yaml:"store_format_version,omitempty"`
	Project            Project     `yaml:"project"`
	Budgets            Budgets     `yaml:"budgets"`
	Updates            Updates     `yaml:"updates"`
	Staging            Staging     `yaml:"staging"`
	Security           Security    `yaml:"security"`
	Git                Git         `yaml:"git"`
	Archive            Archive     `yaml:"archive"`
	Concurrency        Concurrency `yaml:"concurrency"`
	LocalState         LocalState  `yaml:"local_state"`
	Sessions           Sessions    `yaml:"sessions"`
	Eval               Eval        `yaml:"eval,omitempty"`
}

// Project identifies the repository the manifest belongs to.
type Project struct {
	Name string `yaml:"name"`
	Root string `yaml:"root"`
}

// Budgets caps various character / line budgets used by fetch_context and
// the size validator.
type Budgets struct {
	BootstrapChars    int `yaml:"bootstrap_chars"`
	FetchContextChars int `yaml:"fetch_context_chars"`
	MaxFileChars      int `yaml:"max_file_chars"`
}

// Updates holds the per-operation approval overrides.
type Updates struct {
	Approval ApprovalPolicy `yaml:"approval"`
}

// ApprovalPolicy maps category / operation kinds to ApprovalMode. The
// distinction between "pitfalls_append" and "pitfalls_replace" lives here
// (not in schema) because it's per-operation, not per-file.
type ApprovalPolicy struct {
	Decisions       schema.ApprovalMode `yaml:"decisions"`
	Conventions     schema.ApprovalMode `yaml:"conventions"`
	Modules         schema.ApprovalMode `yaml:"modules"`
	PitfallsReplace schema.ApprovalMode `yaml:"pitfalls_replace"`
	PitfallsAppend  schema.ApprovalMode `yaml:"pitfalls_append"`
	Archive         schema.ApprovalMode `yaml:"archive"`
	Current         schema.ApprovalMode `yaml:"current"`
	CurrentShared   schema.ApprovalMode `yaml:"current_shared"`
	Sessions        schema.ApprovalMode `yaml:"sessions"`
	Index           schema.ApprovalMode `yaml:"index"`
}

// Staging holds staging-directory policy.
type Staging struct {
	TTLSeconds int `yaml:"ttl_seconds"`
}

// Security holds security-engine policy.
type Security struct {
	SecretScan                    bool                `yaml:"secret_scan"`
	RejectUntrustedDurableUpdates bool                `yaml:"reject_untrusted_durable_updates"`
	PIIScan                       bool                `yaml:"pii_scan"`
	PIIScanEmail                  bool                `yaml:"pii_scan_email,omitempty"`
	AllowlistLimits               AllowlistLimitsSpec `yaml:"allowlist_limits,omitempty"`
}

// AllowlistLimitsSpec caps how much content allowlist-marker regions
// can cover in a single file. A limit of 0 means "disabled".
//
// The allowlist mechanism is intentionally a per-region escape hatch
// for documenting token formats (`ghp_AaBbCc...example, not real`).
// Without limits, a malicious or careless agent could wrap multi-KB
// regions around real credentials and bypass the scanner entirely.
// These caps keep allowlists in their intended size range.
//
// Defaults (DefaultManifest):
//
//	MaxBytesPerFile:   1024 — five 200-byte format examples fit comfortably
//	MaxRegionsPerFile: 10   — more regions usually signals over-escaping
//	MaxBytesPerRegion: 512  — single largest region; a token + surrounding prose
type AllowlistLimitsSpec struct {
	MaxBytesPerFile   int `yaml:"max_bytes_per_file,omitempty"`
	MaxRegionsPerFile int `yaml:"max_regions_per_file,omitempty"`
	MaxBytesPerRegion int `yaml:"max_bytes_per_region,omitempty"`
}

// Git holds git-integration settings.
type Git struct {
	TrackLocal           bool   `yaml:"track_local"`
	TrackSessions        bool   `yaml:"track_sessions"`
	AutoStageChanges     bool   `yaml:"auto_stage_changes"`
	AutoCommit           bool   `yaml:"auto_commit"`
	CommitMessagePrefix  string `yaml:"commit_message_prefix"`
	MergeDriverInstalled bool   `yaml:"merge_driver_installed"`
}

// Archive holds archival-compaction settings.
type Archive struct {
	Enabled            bool `yaml:"enabled"`
	StaleThresholdDays int  `yaml:"stale_threshold_days"`
}

// Concurrency holds lock-related tunables.
//
// LockTTLSeconds is accepted from legacy v0.4 manifests but intentionally
// IGNORED by the v0.4.1 lock implementation. v0.4.1 §11 replaced TTL-based
// locking with OS-level advisory locks (gofrs/flock); the kernel handles
// release on process death, so application-level TTL has nothing to enforce.
// The field is tagged omitempty so fresh manifests don't carry it forward.
type Concurrency struct {
	LockTTLSeconds     int `yaml:"lock_ttl_seconds,omitempty"`
	WaitTimeoutSeconds int `yaml:"wait_timeout_seconds"`
}

// LocalState holds per-branch local-state behaviour.
type LocalState struct {
	PerBranch                 bool `yaml:"per_branch"`
	SharedFileEnabled         bool `yaml:"shared_file_enabled"`
	CleanupOrphansWarningDays int  `yaml:"cleanup_orphans_warning_days"`
}

// Sessions holds session-recording behaviour.
type Sessions struct {
	PerBranch bool `yaml:"per_branch"`
}

// Eval is optional and used by the M8 benchmark runner.
type Eval struct {
	BenchmarkCorpusURL string `yaml:"benchmark_corpus_url,omitempty"`
}

// DefaultManifest returns the recommended manifest from design doc §26.1.
func DefaultManifest() *Manifest {
	return &Manifest{
		Version:            "0.4.1",
		StoreFormatVersion: CurrentStoreFormatVersion,
		Project:            Project{Root: "."},
		Budgets: Budgets{
			BootstrapChars:    12000,
			FetchContextChars: 24000,
			MaxFileChars:      20000,
		},
		Updates: Updates{
			Approval: ApprovalPolicy{
				Decisions:       schema.ApprovalStage,
				Conventions:     schema.ApprovalStage,
				Modules:         schema.ApprovalStage,
				PitfallsReplace: schema.ApprovalStage,
				PitfallsAppend:  schema.ApprovalApply,
				Archive:         schema.ApprovalStage,
				Current:         schema.ApprovalApply,
				CurrentShared:   schema.ApprovalApply,
				Sessions:        schema.ApprovalApply,
				Index:           schema.ApprovalServerOnly,
			},
		},
		Staging: Staging{TTLSeconds: 604800}, // 7 days
		Security: Security{
			SecretScan:                    true,
			RejectUntrustedDurableUpdates: true,
			PIIScan:                       true,  // SSN + credit-card-Luhn; rare in legitimate text
			PIIScanEmail:                  false, // opt-in: emails appear in legitimate documentation
			AllowlistLimits: AllowlistLimitsSpec{
				MaxBytesPerFile:   1024,
				MaxRegionsPerFile: 10,
				MaxBytesPerRegion: 512,
			},
		},
		Git: Git{
			CommitMessagePrefix: "chore(memory):",
		},
		Archive: Archive{
			Enabled:            true,
			StaleThresholdDays: 60,
		},
		Concurrency: Concurrency{
			WaitTimeoutSeconds: 10,
		},
		LocalState: LocalState{
			PerBranch:                 true,
			SharedFileEnabled:         true,
			CleanupOrphansWarningDays: 90,
		},
		Sessions: Sessions{PerBranch: false},
	}
}

// LoadManifest reads manifest.yaml from path. Missing fields are filled
// from DefaultManifest — yaml.v3 merges into the pre-populated struct, so
// users can override just the fields they care about.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadManifest: %w", err)
	}
	m := DefaultManifest()
	if err := yaml.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("LoadManifest: parse %q: %w", path, err)
	}
	if err := migrateManifest(m); err != nil {
		return nil, fmt.Errorf("LoadManifest: %w", err)
	}
	return m, nil
}

// WriteManifest serialises m to path atomically.
func WriteManifest(path string, m *Manifest) error {
	b, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("WriteManifest: marshal: %w", err)
	}
	return agentfs.WriteAtomic(path, b, 0644)
}

// WriteDefault writes the recommended manifest to path, with Project.Name
// set to projectName. Used by `agent-memory init` (T1.10).
func WriteDefault(path, projectName string) error {
	m := DefaultManifest()
	m.Project.Name = projectName
	return WriteManifest(path, m)
}

// Validate checks basic invariants on m:
//   - Version is non-empty.
//   - Every approval mode is a recognised ApprovalMode.
//   - Budgets and TTL values are positive.
//
// Heavier semantic checks (e.g., that Index is server_only) live in the
// downstream code that consumes the manifest, not in the loader.
func (m *Manifest) Validate() error {
	if m.Version == "" {
		return errors.New("manifest: version is required")
	}

	modes := map[string]schema.ApprovalMode{
		"decisions":        m.Updates.Approval.Decisions,
		"conventions":      m.Updates.Approval.Conventions,
		"modules":          m.Updates.Approval.Modules,
		"pitfalls_replace": m.Updates.Approval.PitfallsReplace,
		"pitfalls_append":  m.Updates.Approval.PitfallsAppend,
		"archive":          m.Updates.Approval.Archive,
		"current":          m.Updates.Approval.Current,
		"current_shared":   m.Updates.Approval.CurrentShared,
		"sessions":         m.Updates.Approval.Sessions,
		"index":            m.Updates.Approval.Index,
	}
	for name, mode := range modes {
		if !mode.IsValid() {
			return fmt.Errorf("manifest: updates.approval.%s = %q: invalid approval mode",
				name, mode)
		}
	}

	if m.Budgets.BootstrapChars <= 0 {
		return errors.New("manifest: budgets.bootstrap_chars must be positive")
	}
	if m.Budgets.FetchContextChars <= 0 {
		return errors.New("manifest: budgets.fetch_context_chars must be positive")
	}
	if m.Budgets.MaxFileChars <= 0 {
		return errors.New("manifest: budgets.max_file_chars must be positive")
	}
	if m.Staging.TTLSeconds <= 0 {
		return errors.New("manifest: staging.ttl_seconds must be positive")
	}
	if m.Concurrency.WaitTimeoutSeconds < 0 {
		return errors.New("manifest: concurrency.wait_timeout_seconds must be non-negative")
	}
	if m.Archive.StaleThresholdDays < 0 {
		return errors.New("manifest: archive.stale_threshold_days must be non-negative")
	}

	return nil
}
