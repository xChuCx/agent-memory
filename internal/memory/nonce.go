package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	agentfs "github.com/xChuCx/agent-memory/internal/fs"
)

var (
	// ErrNonceNotFound indicates the read_nonce is unknown or expired.
	ErrNonceNotFound = errors.New("read_nonce not found or expired")
	// ErrNonceConsumed indicates the read_nonce has already been used (one-shot invariant).
	ErrNonceConsumed = errors.New("read_nonce has already been consumed")
	// ErrDigestMismatch indicates the pack_digest provided does not match the issued pack.
	ErrDigestMismatch = errors.New("pack_digest does not match issued context pack")
)

// NonceRecord tracks an issued proof-of-ingestion capability.
type NonceRecord struct {
	Nonce      string    `json:"nonce"`
	PackDigest string    `json:"pack_digest"`
	IssuedAt   time.Time `json:"issued_at"`
	Consumed   bool      `json:"consumed"`
}

const (
	DefaultNonceTTL    = 10 * time.Minute
	DefaultNonceMaxCap = 1000
)

// NonceStore provides thread-safe and repo-scoped persistent lifecycle tracking for SAR-008 freshness tokens.
type NonceStore struct {
	mu          sync.Mutex
	nonces      map[string]*NonceRecord
	ttl         time.Duration
	storagePath string
	maxCapacity int
}

// NewNonceStore creates a new NonceStore with the given TTL.
func NewNonceStore(ttl time.Duration) *NonceStore {
	if ttl <= 0 {
		ttl = DefaultNonceTTL
	}
	return &NonceStore{
		nonces:      make(map[string]*NonceRecord),
		ttl:         ttl,
		maxCapacity: DefaultNonceMaxCap,
	}
}

// DefaultNonceStore is the active token registry.
var DefaultNonceStore = NewNonceStore(DefaultNonceTTL)

// SetStorageDir configures the persistent store path within the given memory directory (.agent-memory/meta/nonces.json).
func (s *NonceStore) SetStorageDir(memDir string) {
	if memDir == "" {
		return
	}
	s.SetStoragePath(filepath.Join(memDir, "meta", "nonces.json"))
}

// SetStoragePath binds the NonceStore to a persistent repo-scoped file.
func (s *NonceStore) SetStoragePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.storagePath = path
	s.loadLocked()
}

func (s *NonceStore) loadLocked() {
	if s.storagePath == "" {
		return
	}
	data, err := os.ReadFile(s.storagePath)
	if err != nil {
		return
	}
	var stored map[string]*NonceRecord
	if err := json.Unmarshal(data, &stored); err == nil && stored != nil {
		now := time.Now()
		for k, rec := range stored {
			// Only keep unexpired records
			if now.Sub(rec.IssuedAt) <= s.ttl {
				s.nonces[k] = rec
			}
		}
	}
}

func (s *NonceStore) saveLocked() {
	if s.storagePath == "" {
		return
	}
	now := time.Now()
	// Sweep expired
	for k, rec := range s.nonces {
		if now.Sub(rec.IssuedAt) > s.ttl {
			delete(s.nonces, k)
		}
	}
	// Bound capacity
	if len(s.nonces) > s.maxCapacity {
		type kv struct {
			k string
			t time.Time
		}
		list := make([]kv, 0, len(s.nonces))
		for k, rec := range s.nonces {
			list = append(list, kv{k, rec.IssuedAt})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].t.Before(list[j].t) })
		excess := len(s.nonces) - s.maxCapacity
		for i := 0; i < excess; i++ {
			delete(s.nonces, list[i].k)
		}
	}

	data, err := json.MarshalIndent(s.nonces, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.storagePath), 0755)
	_ = agentfs.WriteAtomic(s.storagePath, data, 0644)
}

// Issue registers a new read nonce bound to a canonical pack digest and persists it to disk.
func (s *NonceStore) Issue(nonce, packDigest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()
	s.nonces[nonce] = &NonceRecord{
		Nonce:      nonce,
		PackDigest: packDigest,
		IssuedAt:   time.Now(),
		Consumed:   false,
	}
	s.saveLocked()
}

// Consume validates and marks a nonce as consumed in a single atomic operation.
// Returns an error if the nonce is missing, expired, already consumed, or digest mismatches.
func (s *NonceStore) Consume(nonce, packDigest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadLocked()

	rec, ok := s.nonces[nonce]
	if !ok {
		return ErrNonceNotFound
	}
	if rec.Consumed {
		return ErrNonceConsumed
	}
	if time.Since(rec.IssuedAt) > s.ttl {
		delete(s.nonces, nonce)
		s.saveLocked()
		return ErrNonceNotFound
	}
	if rec.PackDigest != "" && packDigest != "" && rec.PackDigest != packDigest {
		return fmt.Errorf("%w: expected %s, got %s", ErrDigestMismatch, rec.PackDigest, packDigest)
	}
	rec.Consumed = true
	s.saveLocked()
	return nil
}

// InvalidateAll flushes all pending nonces when memory is mutated and clears disk state.
func (s *NonceStore) InvalidateAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nonces = make(map[string]*NonceRecord)
	if s.storagePath != "" {
		_ = os.Remove(s.storagePath)
	}
}
