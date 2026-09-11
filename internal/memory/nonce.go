package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNonceNotFound indicates the read_nonce is unknown or expired.
	ErrNonceNotFound = errors.New("read_nonce not found or expired")
	// ErrNonceConsumed indicates the read_nonce has already been used (one-shot invariant).
	ErrNonceConsumed = errors.New("read_nonce has already been consumed")
	// ErrDigestMismatch indicates the pack_digest provided does not match the issued pack.
	ErrDigestMismatch = errors.New("pack_digest does not match issued context pack")
)

const (
	DefaultNonceTTL    = 10 * time.Minute
	DefaultNonceMaxCap = 1000
)

// NonceStore provides SQLite-backed atomic cross-process single-use token tracking (SAR-008).
type NonceStore struct {
	mu          sync.Mutex
	db          *sql.DB
	ttl         time.Duration
	storagePath string
	maxCapacity int
}

// NewNonceStore creates a new NonceStore with the given TTL.
func NewNonceStore(ttl time.Duration) *NonceStore {
	if ttl <= 0 {
		ttl = DefaultNonceTTL
	}
	s := &NonceStore{
		ttl:         ttl,
		maxCapacity: DefaultNonceMaxCap,
	}
	db, err := openNonceDB(":memory:")
	if err == nil {
		s.db = db
	}
	return s
}

// openNonceDB opens an SQLite database with WAL mode, busy timeout, and the nonces schema.
func openNonceDB(dsn string) (*sql.DB, error) {
	connStr := dsn
	if !strings.Contains(connStr, "_txlock=") {
		if strings.Contains(connStr, "?") {
			connStr += "&_txlock=immediate"
		} else {
			connStr += "?_txlock=immediate"
		}
	}
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", dsn, err)
	}
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA busy_timeout = 5000;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
	}
	for _, p := range pragmas {
		_, _ = db.Exec(p)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS nonces (
		nonce       TEXT PRIMARY KEY,
		pack_digest TEXT NOT NULL,
		issued_at   INTEGER NOT NULL,
		consumed_at INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_nonces_issued_at ON nonces(issued_at);
	`
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init nonces schema: %w", err)
	}
	return db, nil
}

// DefaultNonceStore is the active process token registry.
var DefaultNonceStore = NewNonceStore(DefaultNonceTTL)

// SetStorageDir binds the NonceStore to a persistent repo-scoped SQLite database (.agent-memory/meta/nonces.sqlite).
func (s *NonceStore) SetStorageDir(memDir string) error {
	if memDir == "" {
		return nil
	}
	return s.SetStoragePath(filepath.Join(memDir, "meta", "nonces.sqlite"))
}

// SetStoragePath binds the NonceStore to an explicit SQLite database path, closing any previous database.
func (s *NonceStore) SetStoragePath(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.storagePath == path {
		return nil
	}

	if s.db != nil {
		_ = s.db.Close()
		s.db = nil
	}
	s.storagePath = path

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return fmt.Errorf("mkdir for nonces db: %w", err)
		}
		db, err := openNonceDB(path)
		if err != nil {
			return err
		}
		_ = db.Close()
	}
	return nil
}

func (s *NonceStore) withDB(fn func(db *sql.DB) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.storagePath == "" {
		if s.db == nil {
			db, err := openNonceDB(":memory:")
			if err != nil {
				return err
			}
			s.db = db
		}
		return fn(s.db)
	}

	db, err := openNonceDB(s.storagePath)
	if err != nil {
		return err
	}
	defer db.Close()

	return fn(db)
}

// Issue registers a new read nonce bound to a canonical pack digest and persists it.
func (s *NonceStore) Issue(nonce, packDigest string) error {
	return s.withDB(func(db *sql.DB) error {
		now := time.Now().UnixNano()
		minIssuedAt := now - s.ttl.Nanoseconds()

		// Sweep expired entries
		_, _ = db.Exec("DELETE FROM nonces WHERE issued_at < ?", minIssuedAt)

		// Enforce bounded capacity
		_, _ = db.Exec(`
			DELETE FROM nonces 
			WHERE nonce NOT IN (
				SELECT nonce FROM nonces ORDER BY issued_at DESC LIMIT ?
			)`, s.maxCapacity)

		_, err := db.Exec(
			"INSERT OR REPLACE INTO nonces (nonce, pack_digest, issued_at, consumed_at) VALUES (?, ?, ?, NULL)",
			nonce, packDigest, now,
		)
		if err != nil {
			return fmt.Errorf("issue nonce: %w", err)
		}
		return nil
	})
}

// Consume validates and marks a nonce as consumed in an atomic cross-process transaction.
// Returns an error if the nonce is missing, expired, already consumed, or digest mismatches.
func (s *NonceStore) Consume(nonce, packDigest string) error {
	return s.withDB(func(db *sql.DB) error {
		now := time.Now().UnixNano()
		minIssuedAt := now - s.ttl.Nanoseconds()

		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("begin immediate tx: %w", err)
		}
		defer tx.Rollback()

		var rowDigest string
		var issuedAt int64
		var consumedAt sql.NullInt64

		err = tx.QueryRow(
			"SELECT pack_digest, issued_at, consumed_at FROM nonces WHERE nonce = ?",
			nonce,
		).Scan(&rowDigest, &issuedAt, &consumedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNonceNotFound
		}
		if err != nil {
			return fmt.Errorf("query nonce: %w", err)
		}

		if issuedAt < minIssuedAt {
			return ErrNonceNotFound
		}
		if consumedAt.Valid {
			return ErrNonceConsumed
		}
		if rowDigest != packDigest {
			return ErrDigestMismatch
		}

		res, err := tx.Exec("UPDATE nonces SET consumed_at = ? WHERE nonce = ? AND consumed_at IS NULL", now, nonce)
		if err != nil {
			return fmt.Errorf("consume update: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		if affected != 1 {
			return ErrNonceConsumed
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit consume: %w", err)
		}
		return nil
	})
}

// InvalidateAll deletes all recorded nonces upon memory mutation.
func (s *NonceStore) InvalidateAll() error {
	return s.withDB(func(db *sql.DB) error {
		_, err := db.Exec("DELETE FROM nonces")
		if err != nil {
			return fmt.Errorf("invalidate all nonces: %w", err)
		}
		return nil
	})
}

// Close releases the underlying SQLite database resources.
func (s *NonceStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.storagePath = ""
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}
