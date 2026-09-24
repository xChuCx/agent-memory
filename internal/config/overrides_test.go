package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateOverlayAdmission_RejectsEventualStore(t *testing.T) {
	m := DefaultManifest()
	m.Project.Name = "proj"
	m.Stores = []Store{
		{Name: "strong-store", Source: "https://git.example/strong", ReadbackConsistency: "strong"},
		{Name: "eventual-store", Source: "https://git.example/eventual", ReadbackConsistency: "eventual"},
	}

	// 1. Target store declared as eventual -> MUST FAIL ADMISSION
	badOverrides := &StoreOverrides{
		Version: 1,
		Overrides: []StoreOverride{
			{
				Store:          "eventual-store",
				UpstreamCommit: "commit-123",
				Key:            "SOME_KEY",
				LocalAlias:     "some-key",
				ApprovedBy:     "steward-alice",
			},
		},
	}
	err := ValidateOverlayAdmission(m, badOverrides)
	if err == nil {
		t.Fatal("expected ValidateOverlayAdmission to reject eventual store, got nil")
	}
	if !strings.Contains(err.Error(), "overlays require \"strong\" readback") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Target store undeclared -> MUST FAIL ADMISSION
	undeclaredOverrides := &StoreOverrides{
		Version: 1,
		Overrides: []StoreOverride{
			{
				Store:          "unknown-store",
				UpstreamCommit: "commit-123",
				Key:            "SOME_KEY",
				ApprovedBy:     "steward-alice",
			},
		},
	}
	if err := ValidateOverlayAdmission(m, undeclaredOverrides); err == nil {
		t.Fatal("expected error for undeclared store, got nil")
	}

	// 3. Target store strong -> MUST PASS ADMISSION
	goodOverrides := &StoreOverrides{
		Version: 1,
		Overrides: []StoreOverride{
			{
				Store:          "strong-store",
				UpstreamCommit: "commit-123",
				Key:            "SOME_KEY",
				LocalAlias:     "some-key",
				ApprovedBy:     "steward-alice",
			},
		},
	}
	if err := ValidateOverlayAdmission(m, goodOverrides); err != nil {
		t.Fatalf("expected strong store to pass admission, got: %v", err)
	}
}

func TestCheckOverlayDrift_FailsClosedOnCommitMismatch(t *testing.T) {
	ov := StoreOverride{
		Store:          "arch-wiki",
		UpstreamCommit: "commit-aaa",
		Key:            "STRASSE",
		ApprovedBy:     "steward-alice",
	}

	// Lock with matching commit -> PASS
	matchingLock := &StoresLock{
		Version: StoresLockVersion,
		Stores: map[string]LockedStore{
			"arch-wiki": {ResolvedCommit: "commit-aaa"},
		},
	}
	if err := CheckOverlayDrift(ov, matchingLock); err != nil {
		t.Fatalf("expected matching commit to pass drift check, got: %v", err)
	}

	// Lock with drifted commit -> FAIL CLOSED with ErrOverlayOutdatedDrift
	driftedLock := &StoresLock{
		Version: StoresLockVersion,
		Stores: map[string]LockedStore{
			"arch-wiki": {ResolvedCommit: "commit-bbb"},
		},
	}
	err := CheckOverlayDrift(ov, driftedLock)
	if err == nil {
		t.Fatal("expected drift check to fail on mismatched commit, got nil")
	}
	if !errors.Is(err, ErrOverlayOutdatedDrift) {
		t.Fatalf("expected ErrOverlayOutdatedDrift, got: %v", err)
	}
}

func TestLoadStoreOverrides_MissingReturnsNil(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "store_overrides.yaml")
	o, err := LoadStoreOverrides(p)
	if err != nil {
		t.Fatalf("unexpected error on missing file: %v", err)
	}
	if o != nil {
		t.Fatalf("expected nil for missing file, got %+v", o)
	}

	content := `version: 1
overrides:
  - store: "arch-wiki"
    upstream_commit: "9f2a81c"
    key: "STRASSE"
    local_alias: "strasse-arch"
    approved_by: "steward-01"
`
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	o2, err := LoadStoreOverrides(p)
	if err != nil {
		t.Fatalf("unexpected error parsing overrides: %v", err)
	}
	if len(o2.Overrides) != 1 || o2.Overrides[0].Key != "STRASSE" {
		t.Fatalf("unexpected parsed overrides: %+v", o2)
	}
}
