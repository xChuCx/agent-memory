package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/memory"
)

func TestDigestCmd_NoMemoryDir(t *testing.T) {
	tmpDir := t.TempDir()
	cmd := NewDigestCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--root", tmpDir})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when .agent-memory/ is missing")
	}
	if !strings.Contains(err.Error(), ".agent-memory/ not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDigestCmd_SuccessAndJSON(t *testing.T) {
	tmpDir := t.TempDir()
	memDir := filepath.Join(tmpDir, ".agent-memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(memDir, "conventions.md"), []byte("# Conventions\nRule 1\n"), 0o644); err != nil {
		t.Fatalf("write conventions.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "decisions.md"), []byte("# Decisions\n<!-- @id:dec-1 -->\n"), 0o644); err != nil {
		t.Fatalf("write decisions.md: %v", err)
	}

	// 1. Text output test
	cmd := NewDigestCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--root", tmpDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "Active Memory Merkle Root:") {
		t.Errorf("expected Merkle Root in output: %s", outStr)
	}
	if !strings.Contains(outStr, "conventions.md") || !strings.Contains(outStr, "decisions.md") {
		t.Errorf("expected leaf filenames in output: %s", outStr)
	}

	// 2. JSON output test
	cmdJSON := NewDigestCmd()
	var stdoutJSON bytes.Buffer
	cmdJSON.SetOut(&stdoutJSON)
	cmdJSON.SetArgs([]string{"--root", tmpDir, "--json"})
	if err := cmdJSON.Execute(); err != nil {
		t.Fatalf("execute --json failed: %v", err)
	}

	var d memory.MemoryDigest
	if err := json.Unmarshal(stdoutJSON.Bytes(), &d); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if d.Schema != "agent_memory_digest/1" {
		t.Errorf("expected schema agent_memory_digest/1, got %s", d.Schema)
	}
	if d.FilesCount != 2 {
		t.Errorf("expected 2 files, got %d", d.FilesCount)
	}

	// 3. Verify flag test: match
	cmdVerifyMatch := NewDigestCmd()
	var stdoutVerify bytes.Buffer
	cmdVerifyMatch.SetOut(&stdoutVerify)
	cmdVerifyMatch.SetArgs([]string{"--root", tmpDir, "--verify", d.RootHash})
	if err := cmdVerifyMatch.Execute(); err != nil {
		t.Fatalf("verify with correct hash failed: %v", err)
	}
	if !strings.Contains(stdoutVerify.String(), "OK: Merkle root verified") {
		t.Errorf("expected OK message in verify output: %s", stdoutVerify.String())
	}

	// 4. Verify flag test: mismatch
	cmdVerifyMismatch := NewDigestCmd()
	var stdoutMismatch bytes.Buffer
	cmdVerifyMismatch.SetOut(&stdoutMismatch)
	cmdVerifyMismatch.SetArgs([]string{"--root", tmpDir, "--verify", "0000000000000000000000000000000000000000000000000000000000000000"})
	if err := cmdVerifyMismatch.Execute(); err == nil {
		t.Fatalf("expected error on hash mismatch")
	}
}
