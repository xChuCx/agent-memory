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

// a minimal but valid landscape-store manifest (LoadManifest fills the rest
// from defaults; Validate then passes).
const landscapeManifest = "version: \"0.4.1\"\nstore_format_version: 1\nproject:\n  name: landscape\n"

// storeFiles returns extra files plus a valid .agent-memory/meta/manifest.yaml
// so the source is recognised as an agent-memory store by sync.
func storeFiles(extra map[string]string) map[string]string {
	files := map[string]string{".agent-memory/meta/manifest.yaml": landscapeManifest}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, c := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newPlainStore writes files into a fresh dir (a non-git source).
func newPlainStore(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, files)
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newGitStore is newPlainStore + an initial commit (a git source).
func newGitStore(t *testing.T, files map[string]string) string {
	t.Helper()
	requireGit(t)
	dir := newPlainStore(t, files)
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "init")
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

func mustAddStore(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := stRun(t, append([]string{"add", "--root", dir}, args...)...); err != nil {
		t.Fatalf("store add: %v", err)
	}
}

func TestSync_GitSource_PinsCommit(t *testing.T) {
	src := newGitStore(t, storeFiles(map[string]string{
		".agent-memory/contracts.md": "# Contracts\n## POST /refunds\n<!-- @id: c-refunds -->\n- kind: http\n",
	}))
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
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
	src := newPlainStore(t, storeFiles(map[string]string{
		".agent-memory/components.md": "# Components\n## svc\n<!-- @id: c-svc -->\n- Owner: team\n",
	}))
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	if out, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	if !exists(cachePath(dir, "platform", "components.md")) {
		t.Fatal("expected cached components.md")
	}
	if ls := lockFor(t, dir).Stores["platform"]; !ls.Unlocked {
		t.Fatalf("local non-git path should be unlocked, got %+v", ls)
	}
}

func TestSync_ReproducesLockedCommit(t *testing.T) {
	src := newGitStore(t, storeFiles(map[string]string{
		".agent-memory/contracts.md": "# C\n## a\n<!-- @id: a -->\n- kind: http\n",
	}))
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	if _, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	c1 := lockFor(t, dir).Stores["platform"].ResolvedCommit
	if c1 == "" {
		t.Fatal("no commit pinned after first sync")
	}

	// Advance the source's default branch.
	writeFiles(t, src, map[string]string{
		".agent-memory/contracts.md": "# C\n## a\n<!-- @id: a -->\n- kind: http\n## b\n<!-- @id: b -->\n- kind: event\n",
	})
	gitRun(t, src, "add", "-A")
	gitRun(t, src, "commit", "-q", "-m", "advance")

	// Without --update, sync reproduces the locked commit (reproducible).
	if _, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if got := lockFor(t, dir).Stores["platform"].ResolvedCommit; got != c1 {
		t.Fatalf("sync without --update should reuse %s, got %s", c1, got)
	}

	// With --update, the pin moves forward.
	if _, err := runSyncCmd(t, "--root", dir, "--update"); err != nil {
		t.Fatalf("sync --update: %v", err)
	}
	if got := lockFor(t, dir).Stores["platform"].ResolvedCommit; got == c1 {
		t.Fatalf("sync --update should move the pin forward from %s", c1)
	}
}

func TestSync_ReconcileRemovesCacheAndLock(t *testing.T) {
	src := newPlainStore(t, storeFiles(map[string]string{".agent-memory/components.md": "# C\n## s\n<!-- @id: s -->\n- Owner: t\n"}))
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	if _, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("sync: %v", err)
	}
	cacheDir := filepath.Join(dir, ".agent-memory", "meta", "cache", "stores", "platform")
	if !exists(cacheDir) {
		t.Fatal("store should be cached after first sync")
	}
	if _, err := stRun(t, "rm", "--root", dir, "--name", "platform"); err != nil {
		t.Fatalf("store rm: %v", err)
	}
	if _, err := runSyncCmd(t, "--root", dir); err != nil {
		t.Fatalf("reconcile sync: %v", err)
	}
	if exists(cacheDir) {
		t.Error("reconcile should remove the cache dir of a removed store")
	}
	if _, present := lockFor(t, dir).Stores["platform"]; present {
		t.Error("reconcile should remove the lock entry of a removed store")
	}
}

func TestSync_RejectsNonStore(t *testing.T) {
	// No meta/manifest.yaml → not an agent-memory store.
	src := newPlainStore(t, map[string]string{".agent-memory/contracts.md": "# C\n"})
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected rejection of a non-store source\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "not an agent-memory store") {
		t.Fatalf("expected a not-a-store reason:\n%s", out)
	}
}

func TestSync_FailsClosedOnFutureStoreVersion(t *testing.T) {
	src := newPlainStore(t, map[string]string{
		".agent-memory/meta/manifest.yaml": "version: \"9.9\"\nstore_format_version: 999\nproject:\n  name: future\n",
		".agent-memory/contracts.md":       "# C\n",
	})
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected fail-closed on a future store-format version\n%s", out)
	}
}

func TestSync_LocalPathRevisionFails(t *testing.T) {
	src := newPlainStore(t, storeFiles(map[string]string{".agent-memory/contracts.md": "# C\n"}))
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src, "--revision", "v1")
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected failure: revision on a local non-git source\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "revision") {
		t.Fatalf("expected a revision reason:\n%s", out)
	}
}

func TestSync_RejectsSymlinkInStore(t *testing.T) {
	src := newPlainStore(t, storeFiles(map[string]string{".agent-memory/contracts.md": "# C\n"}))
	if err := os.Symlink(
		filepath.Join(src, ".agent-memory", "contracts.md"),
		filepath.Join(src, ".agent-memory", "link.md"),
	); err != nil {
		t.Skip("symlink unsupported on this platform: " + err.Error())
	}
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected sync to fail on a symlink in the store\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "symlink") {
		t.Fatalf("expected a symlink reason in output:\n%s", out)
	}
}

func TestSync_RejectsSecretOnIngest(t *testing.T) {
	src := newPlainStore(t, storeFiles(map[string]string{
		".agent-memory/notes.md": "# Notes\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n",
	}))
	dir := stInit(t)
	mustAddStore(t, dir, "--name", "platform", "--source", src)
	out, err := runSyncCmd(t, "--root", dir)
	if err == nil {
		t.Fatalf("expected sync to reject a store containing a secret\n%s", out)
	}
	if !strings.Contains(out, "finding") {
		t.Fatalf("expected a scan-finding reason in output:\n%s", out)
	}
	if exists(filepath.Join(dir, ".agent-memory", "meta", "cache", "stores", "platform")) {
		t.Error("a rejected store must not leave a cache dir")
	}
}
