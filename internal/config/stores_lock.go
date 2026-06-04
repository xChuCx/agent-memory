package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

// stores.lock pins each referenced store to a resolved commit so a team/CI
// sees identical landscape memory (design doc §6.1) — analogous to go.sum.
// It is committed (unlike the rebuildable meta/cache/). PR3 (sync) populates
// it; PR2 only defines the format + I/O + status reads it.

// StoresLockVersion is the lockfile format version.
const StoresLockVersion = 1

// StoresLockName is the committed lockfile, under meta/.
const StoresLockName = "stores.lock"

// StoresLock is the deserialisation target for meta/stores.lock.
type StoresLock struct {
	Version int                    `yaml:"version"`
	Stores  map[string]LockedStore `yaml:"stores"`
}

// LockedStore records the resolution of one store at sync time.
type LockedStore struct {
	Source            string `yaml:"source"`
	RequestedRevision string `yaml:"requested_revision,omitempty"`
	ResolvedCommit    string `yaml:"resolved_commit,omitempty"` // empty when Unlocked
	ResolvedAt        string `yaml:"resolved_at,omitempty"`     // RFC 3339; set by sync
	StorePath         string `yaml:"store_path,omitempty"`
	// Unlocked marks a local-path source that is not a git work tree: its
	// content is not pinned to a commit, so the entry is not reproducible.
	// Intended for dev / monorepo use; status surfaces a warning.
	Unlocked bool `yaml:"unlocked,omitempty"`
}

// NewStoresLock returns an empty, current-version lock.
func NewStoresLock() *StoresLock {
	return &StoresLock{Version: StoresLockVersion, Stores: map[string]LockedStore{}}
}

// LoadStoresLock reads meta/stores.lock. A missing file is not an error — it
// means nothing has been synced yet, so an empty lock is returned. A lock
// written by a newer format fails closed.
func LoadStoresLock(path string) (*StoresLock, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewStoresLock(), nil
		}
		return nil, fmt.Errorf("LoadStoresLock: %w", err)
	}
	l := NewStoresLock()
	if err := yaml.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("LoadStoresLock: parse %q: %w", path, err)
	}
	if l.Version > StoresLockVersion {
		return nil, fmt.Errorf("LoadStoresLock: lock version %d > supported %d: %w",
			l.Version, StoresLockVersion, ErrStoreFormatTooNew)
	}
	if l.Stores == nil {
		l.Stores = map[string]LockedStore{}
	}
	return l, nil
}

// WriteStoresLock serialises l to path atomically. yaml.v3 emits map keys in
// sorted order, so the committed lock is deterministic.
func WriteStoresLock(path string, l *StoresLock) error {
	if l.Version == 0 {
		l.Version = StoresLockVersion
	}
	b, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("WriteStoresLock: marshal: %w", err)
	}
	return agentfs.WriteAtomic(path, b, 0644)
}
