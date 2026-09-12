package memory

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xChuCx/agent-memory/internal/schema"
)

func TestIsValidConfidence(t *testing.T) {
	cases := map[string]bool{
		"":              true, // empty allowed
		"confirmed":     true,
		"inferred":      true,
		"user-provided": true,
		"stale":         true,
		"unknown":       true,
		"definitely":    false,
		"Confirmed":     false, // case-sensitive
	}
	for in, want := range cases {
		if got := IsValidConfidence(in); got != want {
			t.Errorf("IsValidConfidence(%q) = %v, want %v", in, got, want)
		}
	}
}

// decisionsPolicy mirrors the recommended schema for decisions:
// sources required, allow only file/test/user, forbid external/inference.
func decisionsPolicy() schema.Provenance {
	return schema.Provenance{
		Required:             true,
		AllowedSourceTypes:   []string{"file", "test", "user"},
		ForbiddenSourceTypes: []string{"external", "inference"},
	}
}

func TestValidateProvenance_HappyPath(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources: []Source{
			{Type: "file", Ref: "internal/auth/refresh.go"},
			{Type: "test", Ref: "internal/auth/refresh_test.go"},
		},
		Confidence: "confirmed",
	})
	if len(v) != 0 {
		t.Errorf("expected no violations, got %v", v)
	}
}

func TestValidateProvenance_RequiredButEmpty(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    nil,
		Confidence: "confirmed",
	})
	if len(v) == 0 {
		t.Fatal("expected violation for missing required sources")
	}
	if !containsSubstr(v, "sources are required") {
		t.Errorf("expected message about required sources: %v", v)
	}
}

func TestValidateProvenance_ForbiddenType(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "external", Ref: "https://blog.example.com"}},
		Confidence: "confirmed",
	})
	if !containsSubstr(v, "forbidden") {
		t.Errorf("expected forbidden-type violation, got %v", v)
	}
}

func TestValidateProvenance_NotInAllowedSet(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "session", Ref: "2026-05-26"}},
		Confidence: "confirmed",
	})
	// "session" is not forbidden but is also not in the allowed list.
	if !containsSubstr(v, "not in allowed") {
		t.Errorf("expected not-in-allowed violation, got %v", v)
	}
}

func TestValidateProvenance_InvalidConfidence(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "file", Ref: "x.go"}},
		Confidence: "definitely",
	})
	if !containsSubstr(v, "confidence") {
		t.Errorf("expected confidence violation, got %v", v)
	}
}

func TestValidateProvenance_EmptyConfidenceAllowed(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "file", Ref: "x.go"}},
		Confidence: "",
	})
	// Empty confidence is fine; treat as "unknown".
	for _, msg := range v {
		if strings.Contains(msg, "confidence") {
			t.Errorf("empty confidence flagged as violation: %v", msg)
		}
	}
}

func TestValidateProvenance_RequiredForNewSections(t *testing.T) {
	policy := schema.Provenance{RequiredForNewSections: true}

	// Empty sources + creating new section → violation.
	v := ValidateProvenance(policy, ProvenanceContext{IsNewSection: true})
	if !containsSubstr(v, "new sections") {
		t.Errorf("expected new-section violation, got %v", v)
	}

	// Empty sources + NOT a new section → OK.
	v = ValidateProvenance(policy, ProvenanceContext{IsNewSection: false})
	if len(v) != 0 {
		t.Errorf("non-new section shouldn't require sources, got %v", v)
	}
}

func TestValidateProvenance_SourceWithoutType(t *testing.T) {
	v := ValidateProvenance(decisionsPolicy(), ProvenanceContext{
		Sources:    []Source{{Type: "", Ref: "something"}},
		Confidence: "confirmed",
	})
	if !containsSubstr(v, "type is required") {
		t.Errorf("expected type-required violation, got %v", v)
	}
}

func TestValidateProvenance_NoSchemaPolicyMeansLooseChecks(t *testing.T) {
	// Empty Provenance policy (no Required, no Allowed/Forbidden lists)
	// → any source is fine.
	v := ValidateProvenance(schema.Provenance{}, ProvenanceContext{
		Sources:    []Source{{Type: "external", Ref: "x"}},
		Confidence: "inferred",
	})
	if len(v) != 0 {
		t.Errorf("unconfigured policy shouldn't violate, got %v", v)
	}
}

func TestValidateProvenance_AllowedWithoutForbiddenStillExcludes(t *testing.T) {
	// AllowedSourceTypes set but ForbiddenSourceTypes empty: anything not
	// in Allowed is rejected as "not in allowed".
	policy := schema.Provenance{
		AllowedSourceTypes: []string{"file"},
	}
	v := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "test", Ref: "x"}},
	})
	if !containsSubstr(v, "not in allowed") {
		t.Errorf("expected not-in-allowed violation, got %v", v)
	}
}

func TestValidateProvenance_BothListsApplied(t *testing.T) {
	// A type in BOTH allowed and forbidden — both messages fire.
	policy := schema.Provenance{
		AllowedSourceTypes:   []string{"file", "test"},
		ForbiddenSourceTypes: []string{"file"},
	}
	v := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "file", Ref: "x"}},
	})
	if !containsSubstr(v, "forbidden") {
		t.Errorf("expected forbidden message, got %v", v)
	}
}

// containsSubstr reports whether any element of haystack contains needle.
func containsSubstr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

func TestValidateGrounding_ValidAndInvalid(t *testing.T) {
	// Nil grounding is valid (optional).
	if viols := ValidateGrounding(nil, false); len(viols) != 0 {
		t.Errorf("expected nil grounding to be valid, got %v", viols)
	}

	// Valid grounding with locator.
	valid := &GroundingReceipt{
		PackDigest: "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
		ReadNonce:  "poi-7f83b165-1757262981000000000",
		Locator:    "decisions.md#ADR-004",
	}
	if viols := ValidateGrounding(valid, true); len(viols) != 0 {
		t.Errorf("expected valid grounding to pass, got %v", viols)
	}

	// Invalid digest prefix.
	badDigest := &GroundingReceipt{
		PackDigest: "md5:123456",
		ReadNonce:  "poi-7f83b165-1757262981000000000",
		Locator:    "decisions.md#ADR-004",
	}
	if viols := ValidateGrounding(badDigest, false); len(viols) == 0 || !containsSubstr(viols, "pack_digest") {
		t.Errorf("expected pack_digest violation, got %v", viols)
	}

	// Invalid nonce prefix.
	badNonce := &GroundingReceipt{
		PackDigest: "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
		ReadNonce:  "invalid-nonce-123",
		Locator:    "decisions.md#ADR-004",
	}
	if viols := ValidateGrounding(badNonce, false); len(viols) == 0 || !containsSubstr(viols, "read_nonce") {
		t.Errorf("expected read_nonce violation, got %v", viols)
	}

	// Missing required locator.
	noLocator := &GroundingReceipt{
		PackDigest: "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
		ReadNonce:  "poi-7f83b165-1757262981000000000",
		Locator:    "",
	}
	if viols := ValidateGrounding(noLocator, true); len(viols) == 0 || !containsSubstr(viols, "locator is required") {
		t.Errorf("expected locator-required violation, got %v", viols)
	}
}

func TestValidateProvenance_WithGrounding(t *testing.T) {
	policy := schema.Provenance{
		Required:           true,
		AllowedSourceTypes: []string{"file"},
	}
	// Invalid grounding propagated into ValidateProvenance.
	viols := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "file", Ref: "x.go"}},
		Grounding: &GroundingReceipt{
			PackDigest: "wrong:prefix",
		},
	})
	if !containsSubstr(viols, "pack_digest") {
		t.Errorf("expected pack_digest violation in ValidateProvenance, got %v", viols)
	}
}

func TestNonceStore_Lifecycle(t *testing.T) {
	store := NewNonceStore(100 * time.Millisecond)
	nonce := "poi-test-12345"
	digest := "sha256:abcd"

	// 1. Issue
	if err := store.Issue(nonce, digest); err != nil {
		t.Fatalf("unexpected issue error: %v", err)
	}

	// 2. Consume with wrong digest fails
	if err := store.Consume(nonce, "sha256:wrong"); err == nil {
		t.Fatal("expected error on digest mismatch, got nil")
	}

	// 3. Consume with correct digest succeeds
	if err := store.Consume(nonce, digest); err != nil {
		t.Fatalf("unexpected consume error: %v", err)
	}

	// 4. One-shot: second consume fails
	if err := store.Consume(nonce, digest); err != ErrNonceConsumed {
		t.Fatalf("expected ErrNonceConsumed, got %v", err)
	}

	// 5. InvalidateAll clears everything
	if err := store.Issue("poi-2", "sha256:2222"); err != nil {
		t.Fatalf("unexpected issue error: %v", err)
	}
	if err := store.InvalidateAll(); err != nil {
		t.Fatalf("unexpected invalidate error: %v", err)
	}
	if err := store.Consume("poi-2", "sha256:2222"); err != ErrNonceNotFound {
		t.Fatalf("expected ErrNonceNotFound after InvalidateAll, got %v", err)
	}
}

func TestNonceStore_Persistent_CrossProcessSyncAndSweep(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "meta", "nonces.sqlite")

	// Process 1 issues a nonce with generous 5-second TTL so slow CI runners don't flake
	store1 := NewNonceStore(5 * time.Second)
	if err := store1.SetStoragePath(storePath); err != nil {
		t.Fatal(err)
	}
	defer store1.Close()

	if err := store1.Issue("poi-proc1", "sha256:1111"); err != nil {
		t.Fatalf("process 1 failed to issue: %v", err)
	}

	// Process 2 opens same SQLite database and consumes the nonce
	store2 := NewNonceStore(5 * time.Second)
	if err := store2.SetStoragePath(storePath); err != nil {
		t.Fatal(err)
	}
	defer store2.Close()

	if err := store2.Consume("poi-proc1", "sha256:1111"); err != nil {
		t.Fatalf("process 2 failed to consume nonce issued by process 1: %v", err)
	}

	// Process 1 attempts to consume already-consumed nonce -> must fail atomically
	if err := store1.Consume("poi-proc1", "sha256:1111"); err != ErrNonceConsumed {
		t.Fatalf("expected ErrNonceConsumed across processes, got %v", err)
	}

	// Concurrent race test: 10 concurrent consumers racing for a single nonce
	if err := store1.Issue("poi-race", "sha256:race"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewNonceStore(5 * time.Second)
			_ = s.SetStoragePath(storePath)
			defer s.Close()
			err := s.Consume("poi-race", "sha256:race")
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successCount != 1 {
		t.Fatalf("expected exactly 1 winner in concurrent race, got %d", successCount)
	}

	// Test auto-sweep after TTL with short-lived store
	sweepStore := NewNonceStore(50 * time.Millisecond)
	sweepPath := filepath.Join(tempDir, "meta", "sweep.sqlite")
	_ = sweepStore.SetStoragePath(sweepPath)
	defer func() { _ = sweepStore.Close() }()

	if err := sweepStore.Issue("poi-short-lived", "sha256:2222"); err != nil {
		t.Fatalf("unexpected issue error: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := sweepStore.Consume("poi-short-lived", "sha256:2222"); err != ErrNonceNotFound {
		t.Fatalf("expected ErrNonceNotFound after TTL expiry, got %v", err)
	}
}

func TestNonceStore_InvalidateAll_RejectsStalePostMutation(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "meta", "nonces.sqlite")

	store := NewNonceStore(10 * time.Minute)
	if err := store.SetStoragePath(storePath); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Issue("poi-context-a", "sha256:aaaa"); err != nil {
		t.Fatal(err)
	}

	// Memory mutation occurs (applyImmediately / ApplyStaged) -> InvalidateAll()
	if err := store.InvalidateAll(); err != nil {
		t.Fatal(err)
	}

	// Propose update using stale nonce must be rejected
	err := store.Consume("poi-context-a", "sha256:aaaa")
	if err != ErrNonceNotFound {
		t.Fatalf("expected ErrNonceNotFound after memory mutation, got %v", err)
	}
}

func TestValidateProvenance_GroundingRequiredPolicy(t *testing.T) {
	policy := schema.Provenance{
		Required:          true,
		GroundingRequired: true,
	}

	// Missing grounding receipt fails
	viols1 := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "file", Ref: "x.go"}},
	})
	if !containsSubstr(viols1, "grounding receipt is required") {
		t.Fatalf("expected grounding required error, got %v", viols1)
	}

	// Grounding receipt without locator fails
	viols2 := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "file", Ref: "x.go"}},
		Grounding: &GroundingReceipt{
			PackDigest: "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
			ReadNonce:  "poi-7f83b165-1757262981000000000",
			Locator:    "",
		},
	})
	if !containsSubstr(viols2, "locator is required") {
		t.Fatalf("expected locator is required error, got %v", viols2)
	}

	// Complete grounding receipt succeeds
	viols3 := ValidateProvenance(policy, ProvenanceContext{
		Sources: []Source{{Type: "file", Ref: "x.go"}},
		Grounding: &GroundingReceipt{
			PackDigest: "sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
			ReadNonce:  "poi-7f83b165-1757262981000000000",
			Locator:    "decisions.md#ADR-004",
		},
	})
	if len(viols3) != 0 {
		t.Fatalf("expected zero violations, got %v", viols3)
	}
}


