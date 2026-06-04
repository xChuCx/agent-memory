package config

import (
	"errors"
	"fmt"
)

// CurrentStoreFormatVersion is the on-disk store-format version this binary
// reads and writes. It is a monotonic integer, deliberately separate from the
// human/product Manifest.Version string: the persisted format evolves on its
// own schedule, and migrations key off this number.
//
// Bump it only when the persisted format changes in a way that needs a
// migration step, and add the corresponding handling in migrateManifest (for
// in-memory normalisation) and/or an explicit store migration (for changes
// that must rewrite files — see migrateManifest's note on why those must not
// run on read paths).
const CurrentStoreFormatVersion = 1

// ErrStoreFormatTooNew is returned when a store declares a format version newer
// than this binary supports. We fail closed rather than risk misreading a
// future layout with an older binary.
var ErrStoreFormatTooNew = errors.New("store format newer than supported; upgrade agent-memory")

// migrateManifest normalises and guards the store-format version on load.
//
//   - absent / 0  → baseline (v1). Pre-1.0 (0.4.x) stores have no
//     store_format_version and ARE format v1, so no on-disk change is needed;
//     LoadManifest seeds the field from DefaultManifest, so absence already
//     resolves to the current baseline — the explicit 0→1 here only covers a
//     manifest that sets the field to 0 by hand.
//   - > current   → fail closed (ErrStoreFormatTooNew).
//   - < current   → reserved for in-memory upgrades as the format evolves
//     (none yet).
//
// IMPORTANT: this runs on every manifest load (i.e. every read path —
// fetch/status/...), so it must stay side-effect-free. Migrations that rewrite
// files belong in an explicit, write-context step (init / a future `migrate`
// command), never here.
func migrateManifest(m *Manifest) error {
	if m.StoreFormatVersion == 0 {
		m.StoreFormatVersion = 1
	}
	if m.StoreFormatVersion > CurrentStoreFormatVersion {
		return fmt.Errorf("store format version %d > supported %d: %w",
			m.StoreFormatVersion, CurrentStoreFormatVersion, ErrStoreFormatTooNew)
	}
	// m.StoreFormatVersion < CurrentStoreFormatVersion: future in-memory upgrades.
	return nil
}
