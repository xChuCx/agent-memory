package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifestYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultManifestCarriesCurrentStoreFormatVersion(t *testing.T) {
	if got := DefaultManifest().StoreFormatVersion; got != CurrentStoreFormatVersion {
		t.Fatalf("DefaultManifest StoreFormatVersion = %d, want %d", got, CurrentStoreFormatVersion)
	}
}

// A 0.4.x-style manifest has no store_format_version; it must load and resolve
// to the baseline (v1) unchanged — the federation/version work must not break
// existing stores.
func TestLoadManifest_LegacyStoreNormalizesToBaseline(t *testing.T) {
	p := writeManifestYAML(t, "version: \"0.4.1\"\nproject:\n  name: legacy\n")
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.StoreFormatVersion != 1 {
		t.Fatalf("legacy store normalized to %d, want 1", m.StoreFormatVersion)
	}
}

func TestLoadManifest_ExplicitZeroNormalizesToBaseline(t *testing.T) {
	p := writeManifestYAML(t, "version: \"0.4.1\"\nstore_format_version: 0\nproject:\n  name: z\n")
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.StoreFormatVersion != 1 {
		t.Fatalf("explicit 0 normalized to %d, want 1", m.StoreFormatVersion)
	}
}

func TestLoadManifest_FutureVersionFailsClosed(t *testing.T) {
	p := writeManifestYAML(t, "version: \"9.9.9\"\nstore_format_version: 999\nproject:\n  name: future\n")
	_, err := LoadManifest(p)
	if err == nil {
		t.Fatal("expected fail-closed error for a future store format, got nil")
	}
	if !errors.Is(err, ErrStoreFormatTooNew) {
		t.Fatalf("error = %v, want errors.Is(..., ErrStoreFormatTooNew)", err)
	}
}

func TestWriteDefault_RoundTripPreservesStoreFormatVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := WriteDefault(p, "rt"); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "store_format_version: 1") {
		t.Fatalf("written manifest missing 'store_format_version: 1':\n%s", raw)
	}
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.StoreFormatVersion != CurrentStoreFormatVersion {
		t.Fatalf("round-trip version = %d, want %d", m.StoreFormatVersion, CurrentStoreFormatVersion)
	}
}
