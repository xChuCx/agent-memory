package cli

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/config"
)

func stInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := runInit(io.Discard, initOptions{Root: dir, ProjectName: "t"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return dir
}

func stRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewStoreCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func stManifest(t *testing.T, dir string) *config.Manifest {
	t.Helper()
	m, err := config.LoadManifest(filepath.Join(dir, ".agent-memory", "meta", "manifest.yaml"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return m
}

func TestStore_AddListRm(t *testing.T) {
	dir := stInit(t)

	if _, err := stRun(t, "add", "--root", dir, "--name", "platform",
		"--source", "https://github.com/acme/platform-memory", "--revision", "v1"); err != nil {
		t.Fatalf("store add: %v", err)
	}
	m := stManifest(t, dir)
	if len(m.Stores) != 1 || m.Stores[0].Name != "platform" || m.Stores[0].Revision != "v1" {
		t.Fatalf("manifest stores after add = %+v", m.Stores)
	}

	out, err := stRun(t, "list", "--root", dir)
	if err != nil {
		t.Fatalf("store list: %v", err)
	}
	if !strings.Contains(out, "platform") || !strings.Contains(out, "not synced") {
		t.Fatalf("list output missing store/lock state:\n%s", out)
	}

	if _, err := stRun(t, "rm", "--root", dir, "--name", "platform"); err != nil {
		t.Fatalf("store rm: %v", err)
	}
	if got := stManifest(t, dir); len(got.Stores) != 0 {
		t.Fatalf("stores after rm = %+v", got.Stores)
	}
}

func TestStore_AddRejectsBadName(t *testing.T) {
	dir := stInit(t)
	if _, err := stRun(t, "add", "--root", dir, "--name", "Bad Name", "--source", "x"); err == nil {
		t.Fatal("expected error for invalid store name")
	}
	if m := stManifest(t, dir); len(m.Stores) != 0 {
		t.Fatalf("invalid store should not persist: %+v", m.Stores)
	}
}

func TestStore_AddRejectsDuplicate(t *testing.T) {
	dir := stInit(t)
	if _, err := stRun(t, "add", "--root", dir, "--name", "p", "--source", "x"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := stRun(t, "add", "--root", dir, "--name", "p", "--source", "y"); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func TestStore_RmUnknown(t *testing.T) {
	dir := stInit(t)
	if _, err := stRun(t, "rm", "--root", dir, "--name", "nope"); err == nil {
		t.Fatal("expected error removing unknown store")
	}
}

func TestStore_ListEmpty(t *testing.T) {
	dir := stInit(t)
	out, err := stRun(t, "list", "--root", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No referenced stores") {
		t.Fatalf("empty list output:\n%s", out)
	}
}

func TestStore_RmCleansLockEntry(t *testing.T) {
	dir := stInit(t)
	if _, err := stRun(t, "add", "--root", dir, "--name", "p", "--source", "x"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Pre-seed a lock entry as `sync` (PR3) eventually will.
	lockPath := filepath.Join(dir, ".agent-memory", "meta", config.StoresLockName)
	lk := config.NewStoresLock()
	lk.Stores["p"] = config.LockedStore{Source: "x", ResolvedCommit: "abc123def456"}
	if err := config.WriteStoresLock(lockPath, lk); err != nil {
		t.Fatalf("seed lock: %v", err)
	}
	if _, err := stRun(t, "rm", "--root", dir, "--name", "p"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	got, err := config.LoadStoresLock(lockPath)
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}
	if _, present := got.Stores["p"]; present {
		t.Fatal("store rm should have removed the lock entry")
	}
}
