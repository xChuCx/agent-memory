package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fptr(f float64) *float64 { return &f }

func TestValidateStores_ViaManifestValidate(t *testing.T) {
	base := func() *Manifest {
		m := DefaultManifest()
		m.Project.Name = "t"
		return m
	}

	cases := []struct {
		name    string
		stores  []Store
		wantErr bool
	}{
		{"valid", []Store{{Name: "platform", Source: "https://x"}}, false},
		{"valid-multi", []Store{{Name: "a", Source: "x"}, {Name: "b-2", Source: "y"}}, false},
		{"valid-priority", []Store{{Name: "a", Source: "x", PriorityMultiplier: fptr(0.5)}}, false},
		{"valid-path", []Store{{Name: "a", Source: "x", Path: "platform/.agent-memory"}}, false},
		{"bad-name-space", []Store{{Name: "Bad Name", Source: "x"}}, true},
		{"bad-name-upper", []Store{{Name: "Platform", Source: "x"}}, true},
		{"duplicate", []Store{{Name: "a", Source: "x"}, {Name: "a", Source: "y"}}, true},
		{"missing-source", []Store{{Name: "a"}}, true},
		{"bad-mode", []Store{{Name: "a", Source: "x", Mode: "read-write"}}, true},
		{"zero-priority", []Store{{Name: "a", Source: "x", PriorityMultiplier: fptr(0)}}, true},
		{"negative-priority", []Store{{Name: "a", Source: "x", PriorityMultiplier: fptr(-1)}}, true},
		{"abs-path", []Store{{Name: "a", Source: "x", Path: "/etc"}}, true},
		{"dotdot-path", []Store{{Name: "a", Source: "x", Path: "../escape"}}, true},
		{"unclean-path", []Store{{Name: "a", Source: "x", Path: "foo/../bar"}}, true},
		{"backslash-path", []Store{{Name: "a", Source: "x", Path: `a\b`}}, true},
		{"drive-path", []Store{{Name: "a", Source: "x", Path: "C:/x"}}, true},
		{"dot-path", []Store{{Name: "a", Source: "x", Path: "."}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			m.Stores = tc.stores
			err := m.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestStoreDefaults(t *testing.T) {
	s := Store{Name: "a", Source: "x"}
	if s.StorePath() != DefaultStorePath {
		t.Errorf("StorePath = %q, want %q", s.StorePath(), DefaultStorePath)
	}
	if s.Priority() != DefaultStorePriority {
		t.Errorf("Priority = %v, want %v", s.Priority(), DefaultStorePriority)
	}
	if s.EffectiveMode() != StoreModeReadOnly {
		t.Errorf("EffectiveMode = %q, want %q", s.EffectiveMode(), StoreModeReadOnly)
	}
	// Explicit overrides are honored.
	s2 := Store{Name: "a", Source: "x", Path: "platform/.agent-memory", PriorityMultiplier: fptr(0.5)}
	if s2.StorePath() != "platform/.agent-memory" || s2.Priority() != 0.5 {
		t.Errorf("overrides not honored: path=%q prio=%v", s2.StorePath(), s2.Priority())
	}
}

// Opt-in invariant: a manifest with no stores omits the block and behaves
// exactly as before.
func TestNoStores_OmittedAndValid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := WriteDefault(p, "nost"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "stores:") {
		t.Fatalf("default manifest should omit empty stores:\n%s", raw)
	}
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(m.Stores) != 0 {
		t.Fatalf("expected 0 stores, got %d", len(m.Stores))
	}
}

func TestStoresLock_RoundTripAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), StoresLockName)

	// Missing lock → empty, current version, no error.
	l, err := LoadStoresLock(p)
	if err != nil {
		t.Fatalf("missing lock load: %v", err)
	}
	if l.Version != StoresLockVersion || len(l.Stores) != 0 {
		t.Fatalf("missing lock not empty/current: %+v", l)
	}

	l.Stores["platform"] = LockedStore{
		Source:         "https://x",
		ResolvedCommit: "abc123def456",
		StorePath:      ".agent-memory",
	}
	if err := WriteStoresLock(p, l); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := LoadStoresLock(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Stores["platform"].ResolvedCommit != "abc123def456" {
		t.Fatalf("round-trip lock = %+v", got.Stores["platform"])
	}
}

func TestStoresLock_FutureVersionFailsClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), StoresLockName)
	if err := os.WriteFile(p, []byte("version: 999\nstores: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStoresLock(p); !errors.Is(err, ErrStoreFormatTooNew) {
		t.Fatalf("expected ErrStoreFormatTooNew, got %v", err)
	}
}

func TestStoresLock_MissingVersionIsMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), StoresLockName)
	if err := os.WriteFile(p, []byte("stores: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStoresLock(p); err == nil {
		t.Fatal("expected error for a versionless (malformed) lock, got nil")
	}
}
