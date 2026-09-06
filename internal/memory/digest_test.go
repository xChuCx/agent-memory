package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestComputeDigest_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	d, err := ComputeDigest(tmpDir)
	if err != nil {
		t.Fatalf("ComputeDigest failed: %v", err)
	}
	if d.FilesCount != 0 {
		t.Errorf("expected 0 files, got %d", d.FilesCount)
	}
	emptyHash := hex.EncodeToString(sha256.New().Sum(nil))
	if d.RootHash != emptyHash {
		t.Errorf("expected empty root %s, got %s", emptyHash, d.RootHash)
	}
}

func TestComputeDigest_CRLF_Invariance(t *testing.T) {
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	// Write CRLF content in dir1
	crlfContent := "line 1\r\nline 2\r\n# Title\r\n"
	if err := os.WriteFile(filepath.Join(tmpDir1, "conventions.md"), []byte(crlfContent), 0o644); err != nil {
		t.Fatalf("write tmpDir1: %v", err)
	}

	// Write LF content in dir2
	lfContent := "line 1\nline 2\n# Title\n"
	if err := os.WriteFile(filepath.Join(tmpDir2, "conventions.md"), []byte(lfContent), 0o644); err != nil {
		t.Fatalf("write tmpDir2: %v", err)
	}

	d1, err := ComputeDigest(tmpDir1)
	if err != nil {
		t.Fatalf("ComputeDigest dir1: %v", err)
	}

	d2, err := ComputeDigest(tmpDir2)
	if err != nil {
		t.Fatalf("ComputeDigest dir2: %v", err)
	}

	if d1.RootHash != d2.RootHash {
		t.Errorf("CRLF invariance violated! dir1=%s, dir2=%s", d1.RootHash, d2.RootHash)
	}
	if d1.Leaves[0].SHA256 != d2.Leaves[0].SHA256 {
		t.Errorf("Leaf hash mismatch! %s vs %s", d1.Leaves[0].SHA256, d2.Leaves[0].SHA256)
	}
}

func TestComputeDigest_MerklePairwise(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"conventions.md":   "conventions content\n",
		"decisions.md":     "decisions content\n",
		"index.md":         "index content\n",
		"pitfalls.md":      "pitfalls content\n",
		"modules/auth.md":  "module auth\n",
		"modules/cache.md": "module cache\n",
	}

	for rel, content := range files {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	d, err := ComputeDigest(tmpDir)
	if err != nil {
		t.Fatalf("ComputeDigest: %v", err)
	}

	if d.FilesCount != 6 {
		t.Fatalf("expected 6 files, got %d", d.FilesCount)
	}

	// Verify paths are sorted lexicographically
	expectedOrder := []string{
		"conventions.md",
		"decisions.md",
		"index.md",
		"modules/auth.md",
		"modules/cache.md",
		"pitfalls.md",
	}
	for i, exp := range expectedOrder {
		if d.Leaves[i].Path != exp {
			t.Errorf("leaf[%d] path expected %s, got %s", i, exp, d.Leaves[i].Path)
		}
	}

	// Verify root hash changes if a single character is modified
	fullPitfalls := filepath.Join(tmpDir, "pitfalls.md")
	if err := os.WriteFile(fullPitfalls, []byte("pitfalls content altered\n"), 0o644); err != nil {
		t.Fatalf("rewrite pitfalls: %v", err)
	}
	dAltered, err := ComputeDigest(tmpDir)
	if err != nil {
		t.Fatalf("ComputeDigest altered: %v", err)
	}
	if dAltered.RootHash == d.RootHash {
		t.Errorf("root hash should have changed after file modification")
	}
}
