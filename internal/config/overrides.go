package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// StoreOverridesName is the filename under meta/ for local resolution overlays (SAR-010.1).
const StoreOverridesName = "store_overrides.yaml"

// StoreOverridesVersion is the current version of the store_overrides format.
const StoreOverridesVersion = 1

// ErrOverlayOutdatedDrift is returned when stores.lock has moved to a commit
// not covered by the overlay's pinned upstream_commit.
var ErrOverlayOutdatedDrift = errors.New("overlay_outdated_drift")

// StoreOverride represents one local resolution overlay over an upstream key.
type StoreOverride struct {
	Store          string `yaml:"store"`
	UpstreamCommit string `yaml:"upstream_commit"`
	Key            string `yaml:"key"`
	LocalAlias     string `yaml:"local_alias,omitempty"`
	Exclude        bool   `yaml:"exclude,omitempty"`
	ApprovedBy     string `yaml:"approved_by"`
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
