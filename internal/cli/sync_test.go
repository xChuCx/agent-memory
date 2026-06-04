package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xChuCx/agent-memory/internal/config"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func runSyncCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewSyncCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// newPlainStore writes files into a fresh dir (a non-git source).
func newPlainStore(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, c := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// newGitStore is newPlainStore + an initial commit (a git source).
func newGitStore(t *testing.T, files map[string]string) string {
	t.Helper()
	requireGit(t)
	dir := newPlainStore(t, files)
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "commit.gpgsign", "false")
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func cachePath(dir, name, rel string) string {
	return filepath.Join(dir, ".agent-memory", "meta", "cache", "stores", name, filepath.FromSlash(rel))
}

func lockFor(t *testing.T, dir string) *config.StoresLock {
	t.Helper()
	l, err := config.LoadStoresLock(filepath.Join(dir, ".agent-memory", "meta", config.StoresLockName))
	if err != nil {
		t.Fatalf("load lock: %v", err)
	}
	return l
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestSync_GitSource_PinsCommit(t *testing.T) {
	src := newGitStore(t, map[string]string{
		".agent-memory/contracts.md": "# Contracts\n## POST /refunds\n<!-- @id: c-refunds -->\n- kind: http\n",
	})
	dir := stInit(t)
	if _, err := stRun(t, "add", "--root", dir, "--name", "platform", "--source", src); err != nil {
		t.Fatalf("store add: %v", err)
	}
	out, err := runSyncCmd(t, "--root", dir)
	if err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !exists(cachePath(dir, "platform", "contracts.md")) {
		t.Fatalf("expected cached contracts.md\n%s", out)
	}
	ls := lockFor(t, dir).Stores["platform"]
	if ls.ResolvedCommit == "" || ls.Unlocked {
		t.Fatalf("git store should be pinned, got %+v", ls)
	}
}

func TestSync_LocalPath_Unlocked(t *testing.T) {
	src := newPlainStore(t, map[string]string{
		".agent-memory/components.md": "# Components\n## svc\n<!-- @id: c-svc -->\n- Owner: team\n",
	})
	dir := stInit(t)
	if _, err := stRun(t, "add", "--root", dir, "--name", "local", "--source", src); err != nil {
		t.Fatalf("store add: %v", err)
	}
	if out, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !exists(cachePath(dir, "local", "components.md")) {
		t.Fatal("expected cached components.md")
	}
	if ls := lockFor(t, dir).Stores["local"]; !ls.Unlocked {
		t.Fatalf("local non-git path should be unlocked, got %+v", ls)
	}
}

func TestSync_ReconcileRemovesCacheAndLock(t *testing.T) {
	src := newPlainStore(t, map[string]string{".agent-memory/components.md": "# C\n## s\n<!-- @id: s -->\n- Owner: t\n"})
	dir := stInit(t)
	stRun(t, "add", "--root", dir, "--name", "local", "--source", src)
	if _, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("sync: %v", err)
	}
	cacheDir := filepath.Join(dir, ".agent-memory", "meta", "cache", "stores", "local")
	if !exists(cacheDir) {
		t.Fatal("store should be cached after first sync")
	}
	// Remove the store, then sync again → reconcile.
	stRun(t, "rm", "--root", dir, "--name", "local")
	if _, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("reconcile sync: %v", err)
	}
	if exists(cacheDir) {
		t.Error("reconcile should remove the cache dir of a removed store")
	}
	if _, present := lockFor(t, dir).Stores["local"]; present {
		t.Error("reconcile should remove the lock entry of a removed store")
	}
}

func TestSync_RejectsSymlinkInStore(t *testing.T) {
	src := newPlainStore(t, map[string]string{".agent-memory/contracts.md": "# C\n"})
	if err := os.Symlink(
		filepath.Join(src, ".agent-memory", "contracts.md"),
		filepath.Join(src, ".agent-memory", "link.md"),
	); err != nil {
		t.Skip("symlink unsupported on this platform: " + err.Error())
	}
	dir := stInit(t)
	stRun(t, "add", "--root", dir, "--name", "local", "--source", src)
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected sync to fail on a symlink in the store\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "symlink") {
		t.Fatalf("expected a symlink reason in output:\n%s", out)
	}
}

func TestSync_RejectsSecretOnIngest(t *testing.T) {
	src := newPlainStore(t, map[string]string{
		".agent-memory/notes.md": "# Notes\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n",
	})
	dir := stInit(t)
	stRun(t, "add", "--root", dir, "--name", "local", "--source", src)
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected sync to reject a store containing a secret\n%s", out)
	}
	if !strings.Contains(out, "finding") {
		t.Fatalf("expected a scan-finding reason in output:\n%s", out)
	}
	// And nothing should have been materialised into the cache.
	if exists(filepath.Join(dir, ".agent-memory", "meta", "cache", "stores", "local")) {
		t.Error("a rejected store must not leave a cache dir")
	}
}
