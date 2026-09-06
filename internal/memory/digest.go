package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileDigestLeaf records the cryptographic footprint of one durable memory file.
type FileDigestLeaf struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

// MemoryDigest captures the deterministic cryptographic state of the active durable memory.
type MemoryDigest struct {
	Schema     string           `json:"schema"`
	RootHash   string           `json:"root"`
	FilesCount int              `json:"files_count"`
	TotalBytes int              `json:"total_bytes"`
	Leaves     []FileDigestLeaf `json:"leaves"`
}

// ComputeDigest walks the durable memory markdown files under memDir, normalizes
// newlines (\r\n -> \n) for cross-platform determinism, computes the SHA-256
// hash of each leaf, and calculates the canonical pairwise SHA-256 Merkle root.
func ComputeDigest(memDir string) (*MemoryDigest, error) {
	var leaves []FileDigestLeaf
	var totalBytes int

	// Canonical root files to look for
	rootFiles := []string{
		"conventions.md",
		"decisions.md",
		"index.md",
		"pitfalls.md",
	}

	for _, rf := range rootFiles {
		p := filepath.Join(memDir, rf)
		leaf, err := hashFileIfPresent(memDir, p, rf)
		if err != nil {
			return nil, err
		}
		if leaf != nil {
			leaves = append(leaves, *leaf)
			totalBytes += leaf.Bytes
		}
	}

	// Also discover modules/*.md
	modulesDir := filepath.Join(memDir, "modules")
	if info, err := os.Stat(modulesDir); err == nil && info.IsDir() {
		err := filepath.WalkDir(modulesDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				rel, err := filepath.Rel(memDir, path)
				if err != nil {
					return err
				}
				relSlash := filepath.ToSlash(rel)
				leaf, err := hashFileIfPresent(memDir, path, relSlash)
				if err != nil {
					return err
				}
				if leaf != nil {
					leaves = append(leaves, *leaf)
					totalBytes += leaf.Bytes
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("ComputeDigest: walk modules: %w", err)
		}
	}

	// Deterministic sorting by normalized relative path
	sort.Slice(leaves, func(i, j int) bool {
		return leaves[i].Path < leaves[j].Path
	})

	rootHash := computeMerkleRoot(leaves)

	return &MemoryDigest{
		Schema:     "agent_memory_digest/1",
		RootHash:   rootHash,
		FilesCount: len(leaves),
		TotalBytes: totalBytes,
		Leaves:     leaves,
	}, nil
}

func hashFileIfPresent(memDir, fullPath, relSlash string) (*FileDigestLeaf, error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("hashFile: %s: %w", fullPath, err)
	}

	// Normalise CRLF to LF to guarantee identical Merkle roots across Windows and Linux
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	sum := sha256.Sum256(normalized)
	h := hex.EncodeToString(sum[:])

	return &FileDigestLeaf{
		Path:   relSlash,
		SHA256: h,
		Bytes:  len(normalized),
	}, nil
}

// computeMerkleRoot builds a canonical pairwise SHA-256 Merkle tree.
// Pairwise rule: parent = sha256(bytes.fromhex(left) + bytes.fromhex(right))
// If odd count, orphan leaf is promoted to the next layer.
func computeMerkleRoot(leaves []FileDigestLeaf) string {
	if len(leaves) == 0 {
		empty := sha256.Sum256([]byte{})
		return hex.EncodeToString(empty[:])
	}
	if len(leaves) == 1 {
		return leaves[0].SHA256
	}

	currentLevel := make([]string, len(leaves))
	for i, l := range leaves {
		currentLevel[i] = l.SHA256
	}

	for len(currentLevel) > 1 {
		var nextLevel []string
		for i := 0; i < len(currentLevel); i += 2 {
			if i+1 < len(currentLevel) {
				leftBytes, _ := hex.DecodeString(currentLevel[i])
				rightBytes, _ := hex.DecodeString(currentLevel[i+1])
				combined := append(leftBytes, rightBytes...)
				parent := sha256.Sum256(combined)
				nextLevel = append(nextLevel, hex.EncodeToString(parent[:]))
			} else {
				// Odd node: promote to next level
				nextLevel = append(nextLevel, currentLevel[i])
			}
		}
		currentLevel = nextLevel
	}

	return currentLevel[0]
}
