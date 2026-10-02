package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// StoreOverridesName is the filename under meta/ for local resolution overlays (SAR-010.1).
const StoreOverridesName = "store_overrides.yaml"

// StoreOverridesVersion is the current version of the store_overrides format.
const StoreOverridesVersion = 1

// ErrOverlayOutdatedDrift is returned when stores.lock has moved to a commit
// not covered by the overlay's pinned upstream_commit.
var ErrOverlayOutdatedDrift = errors.New("overlay_outdated_drift")

// ErrLockModifiedCAS is returned when the on-disk lockfile digest no longer matches
// the expected digest captured when admission/drift checks were executed.
var ErrLockModifiedCAS = errors.New("lockfile_modified_concurrently")

// StoreOverride represents one local resolution overlay over an upstream key.
type StoreOverride struct {
	IncidentID         string `yaml:"incident_id"`
	Store              string `yaml:"store"`
	UpstreamCommit     string `yaml:"upstream_commit"`
	Key                string `yaml:"key"`
	LocalAlias         string `yaml:"local_alias,omitempty"`
	Exclude            bool   `yaml:"exclude,omitempty"`
	ApprovedBy         string `yaml:"approved_by"`
	AssignedSteward    string `yaml:"assigned_steward"`
	EscalationDeadline string `yaml:"escalation_deadline"`
}

// StoreOverrides is the root structure of meta/store_overrides.yaml.
type StoreOverrides struct {
	Version   int             `yaml:"version"`
	Overrides []StoreOverride `yaml:"overrides"`
}

// LoadStoreOverrides reads meta/store_overrides.yaml. A missing file returns nil without error.
func LoadStoreOverrides(path string) (*StoreOverrides, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("LoadStoreOverrides: %w", err)
	}
	var o StoreOverrides
	if err := yaml.Unmarshal(b, &o); err != nil {
		return nil, fmt.Errorf("LoadStoreOverrides: parse %q: %w", path, err)
	}
	return &o, nil
}

// ValidateOverlayAdmission enforces SAR-010.1 overlay admission invariants:
// 1. Every override must target a declared store in manifest.Stores.
// 2. The targeted store MUST have readback_consistency == "strong". An eventual-consistency
//    store is strictly refused admission to prevent verifying against a stale lock view.
// 3. UpstreamCommit and ApprovedBy are mandatory for W-fact cryptographic tracking.
// 4. Governance fields IncidentID, AssignedSteward, and EscalationDeadline are mandatory,
//    and EscalationDeadline must be a valid RFC3339 timestamp.
func ValidateOverlayAdmission(m *Manifest, o *StoreOverrides) error {
	if o == nil || len(o.Overrides) == 0 {
		return nil
	}
	if m == nil {
		return errors.New("manifest is required to validate overlay admission")
	}

	storeMap := make(map[string]Store, len(m.Stores))
	for _, s := range m.Stores {
		storeMap[s.Name] = s
	}

	for i, ov := range o.Overrides {
		st, exists := storeMap[ov.Store]
		if !exists {
			return fmt.Errorf("overlay[%d]: store %q is not declared in manifest.yaml", i, ov.Store)
		}
		if st.EffectiveReadbackConsistency() != ReadbackConsistencyStrong {
			return fmt.Errorf("overlay[%d]: store %q has readback_consistency: %q; overlays require %q readback to prevent stale lock view (SAR-010.1)",
				i, ov.Store, st.EffectiveReadbackConsistency(), ReadbackConsistencyStrong)
		}
		if ov.UpstreamCommit == "" {
			return fmt.Errorf("overlay[%d]: upstream_commit is required for W-fact binding", i)
		}
		if ov.ApprovedBy == "" {
			return fmt.Errorf("overlay[%d]: approved_by is required for governance tracking", i)
		}
		if ov.Key == "" {
			return fmt.Errorf("overlay[%d]: key is required", i)
		}
		if ov.IncidentID == "" {
			return fmt.Errorf("overlay[%d]: incident_id is required for governance tracking (SAR-010.1)", i)
		}
		if ov.AssignedSteward == "" {
			return fmt.Errorf("overlay[%d]: assigned_steward is required for governance accountability (SAR-010.1)", i)
		}
		if ov.EscalationDeadline == "" {
			return fmt.Errorf("overlay[%d]: escalation_deadline is required (SAR-010.1)", i)
		}
		parsedTime, err := time.Parse(time.RFC3339, ov.EscalationDeadline)
		if err != nil {
			return fmt.Errorf("overlay[%d]: escalation_deadline %q must be valid RFC3339 timestamp: %w", i, ov.EscalationDeadline, err)
		}
		// Canonicalize to UTC RFC3339 (e.g. 2026-10-01T00:00:00Z) to prevent
		// semantic moment vs literal string spelling divergence (Class 251).
		o.Overrides[i].EscalationDeadline = parsedTime.UTC().Format(time.RFC3339)
	}
	return nil
}

// CheckOverlayDrift verifies that the locked commit in stores.lock matches the
// overlay's pinned UpstreamCommit. Fails closed with ErrOverlayOutdatedDrift if
// the lock has moved or the store is missing.
func CheckOverlayDrift(ov StoreOverride, lock *StoresLock) error {
	if lock == nil || lock.Stores == nil {
		return fmt.Errorf("stores.lock missing or empty: %w", ErrOverlayOutdatedDrift)
	}
	locked, ok := lock.Stores[ov.Store]
	if !ok {
		return fmt.Errorf("store %q not found in stores.lock: %w", ov.Store, ErrOverlayOutdatedDrift)
	}
	if locked.ResolvedCommit != ov.UpstreamCommit {
		return fmt.Errorf("commit drift detected: store %q locked at %q, overlay pinned to %q: %w",
			ov.Store, locked.ResolvedCommit, ov.UpstreamCommit, ErrOverlayOutdatedDrift)
	}
	return nil
}

// ComputeStoresLockDigest calculates the SHA-256 digest of the lock file at path.
// If the file does not exist, returns ("", nil).
func ComputeStoresLockDigest(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("ComputeStoresLockDigest: %w", err)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// VerifyStoresLockCAS verifies that the on-disk lockfile has not been modified
// since expectedDigest was computed.
func VerifyStoresLockCAS(path string, expectedDigest string) error {
	curDigest, err := ComputeStoresLockDigest(path)
	if err != nil {
		return err
	}
	if curDigest != expectedDigest {
		return fmt.Errorf("CAS validation failed: expected lock digest %s, found %s: %w",
			expectedDigest, curDigest, ErrLockModifiedCAS)
	}
	return nil
}
