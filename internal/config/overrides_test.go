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
				IncidentID:         "INC-101",
				Store:              "eventual-store",
				UpstreamCommit:     "commit-123",
				Key:                "SOME_KEY",
				LocalAlias:         "some-key",
				ApprovedBy:         "steward-alice",
				AssignedSteward:    "steward-alice",
				EscalationDeadline: "2026-10-31T00:00:00Z",
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
				IncidentID:         "INC-102",
				Store:              "unknown-store",
				UpstreamCommit:     "commit-123",
				Key:                "SOME_KEY",
				ApprovedBy:         "steward-alice",
				AssignedSteward:    "steward-alice",
				EscalationDeadline: "2026-10-31T00:00:00Z",
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
				IncidentID:         "INC-103",
				Store:              "strong-store",
				UpstreamCommit:     "commit-123",
				Key:                "SOME_KEY",
				LocalAlias:         "some-key",
				ApprovedBy:         "steward-alice",
				AssignedSteward:    "steward-alice",
				EscalationDeadline: "2026-10-31T00:00:00Z",
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

func TestVerifyStoresLockCAS_DetectsInterleavedMutation(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "stores.lock")

	// 1. Initial lockfile state A
	if err := os.WriteFile(lockPath, []byte("version: 1\nstores:\n  repo-a:\n    commit: commit-aaa\n"), 0644); err != nil {
		t.Fatal(err)
	}

	digestA, err := ComputeStoresLockDigest(lockPath)
	if err != nil {
		t.Fatalf("ComputeStoresLockDigest: %v", err)
	}
	if digestA == "" {
		t.Fatal("expected non-empty digest for existing lockfile")
	}

	// CAS check against unchanged file passes
	if err := VerifyStoresLockCAS(lockPath, digestA); err != nil {
		t.Fatalf("expected CAS check to pass on unchanged file: %v", err)
	}

	// 2. Interleaved concurrent modification to state B
	if err := os.WriteFile(lockPath, []byte("version: 1\nstores:\n  repo-a:\n    commit: commit-bbb\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// CAS check with digest A now MUST FAIL with ErrLockModifiedCAS
	err = VerifyStoresLockCAS(lockPath, digestA)
	if err == nil {
		t.Fatal("expected VerifyStoresLockCAS to fail after concurrent modification, got nil")
	}
	if !errors.Is(err, ErrLockModifiedCAS) {
		t.Fatalf("expected ErrLockModifiedCAS, got: %v", err)
	}
}

func TestVerifyStoresLockCAS_FailsWhenInitialMissingFileCreatedConcurrently(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "stores.lock")

	// 1. Initial lockfile did not exist -> digest is ""
	digestEmpty, err := ComputeStoresLockDigest(lockPath)
	if err != nil {
		t.Fatalf("ComputeStoresLockDigest: %v", err)
	}
	if digestEmpty != "" {
		t.Fatalf("expected empty digest for non-existent file, got %q", digestEmpty)
	}

	// Unchanged non-existent file passes CAS
	if err := VerifyStoresLockCAS(lockPath, digestEmpty); err != nil {
		t.Fatalf("expected CAS check to pass for non-existent file, got: %v", err)
	}

	// 2. Interleaved creation by another process
	if err := os.WriteFile(lockPath, []byte("version: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// CAS check with empty digest MUST FAIL with ErrLockModifiedCAS
	err = VerifyStoresLockCAS(lockPath, digestEmpty)
	if err == nil {
		t.Fatal("expected VerifyStoresLockCAS to fail when missing file is created concurrently, got nil")
	}
	if !errors.Is(err, ErrLockModifiedCAS) {
		t.Fatalf("expected ErrLockModifiedCAS, got: %v", err)
	}
}

func TestValidateOverlayAdmission_EnforcesGovernanceFields(t *testing.T) {
	m := DefaultManifest()
	m.Stores = []Store{
		{Name: "strong-store", Source: "https://git.example/strong", ReadbackConsistency: "strong"},
	}

	base := StoreOverride{
		IncidentID:         "INC-2026-09",
		Store:              "strong-store",
		UpstreamCommit:     "commit-123",
		Key:                "SOME_KEY",
		ApprovedBy:         "steward-alice",
		AssignedSteward:    "steward-bob",
		EscalationDeadline: "2026-10-15T12:00:00Z",
	}

	// Missing IncidentID
	ov1 := base
	ov1.IncidentID = ""
	if err := ValidateOverlayAdmission(m, &StoreOverrides{Version: 1, Overrides: []StoreOverride{ov1}}); err == nil {
		t.Fatal("expected error for missing IncidentID, got nil")
	}

	// Missing AssignedSteward
	ov2 := base
	ov2.AssignedSteward = ""
	if err := ValidateOverlayAdmission(m, &StoreOverrides{Version: 1, Overrides: []StoreOverride{ov2}}); err == nil {
		t.Fatal("expected error for missing AssignedSteward, got nil")
	}

	// Missing EscalationDeadline
	ov3 := base
	ov3.EscalationDeadline = ""
	if err := ValidateOverlayAdmission(m, &StoreOverrides{Version: 1, Overrides: []StoreOverride{ov3}}); err == nil {
		t.Fatal("expected error for missing EscalationDeadline, got nil")
	}

	// Invalid EscalationDeadline format (not RFC3339)
	ov4 := base
	ov4.EscalationDeadline = "2026-10-15 12:00:00"
	if err := ValidateOverlayAdmission(m, &StoreOverrides{Version: 1, Overrides: []StoreOverride{ov4}}); err == nil {
		t.Fatal("expected error for non-RFC3339 EscalationDeadline, got nil")
	}

	// Valid overlay passes
	if err := ValidateOverlayAdmission(m, &StoreOverrides{Version: 1, Overrides: []StoreOverride{base}}); err != nil {
		t.Fatalf("expected valid overlay to pass, got: %v", err)
	}
}


