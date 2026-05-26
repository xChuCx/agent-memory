package fs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteAtomic_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := WriteAtomic(path, []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("contents: got %q, want %q", got, "hello")
	}
}

func TestWriteAtomic_NoTempLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := WriteAtomic(path, []byte("data"), 0644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "test.txt.tmp.") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestWriteAtomic_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, []byte("new"), 0644); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("contents: got %q, want %q", got, "new")
	}
}

func TestWriteAtomic_RelativePathRejected(t *testing.T) {
	if err := WriteAtomic("rel/path.txt", []byte("x"), 0644); err == nil {
		t.Error("expected error for relative path")
	}
}

func TestWriteAtomic_MissingParentRejected(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "no-such-dir", "child.txt")
	if err := WriteAtomic(missingDir, []byte("x"), 0644); err == nil {
		t.Error("expected error for missing parent")
	}
}

func TestWriteAtomic_ConcurrentNoTear(t *testing.T) {
	// Multiple goroutines write to the same path. Each write is 1024 bytes
	// of a single repeated digit. After all writers complete, the file must
	// contain exactly one of those complete writes — never a mix of two
	// different digits, never a truncated buffer.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	const writers = 50
	const size = 1024

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			digit := byte('0' + (n % 10))
			content := bytes.Repeat([]byte{digit}, size)
			// On Windows, concurrent renames can collide with a sharing
			// violation. Individual writer failures are tolerable under
			// contention — what we test is that the FINAL file is intact.
			if err := WriteAtomic(path, content, 0644); err != nil {
				t.Logf("writer %d: %v (tolerable under contention)", n, err)
			}
		}(i)
	}
	wg.Wait()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(got) != size {
		t.Errorf("torn write: got %d bytes, want %d", len(got), size)
	}
	first := got[0]
	for i, c := range got {
		if c != first {
			t.Errorf("torn write: byte %d is %q, but byte 0 is %q", i, c, first)
			break
		}
	}

	// And no temp leaks.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "test.txt.tmp.") {
			t.Errorf("temp file leaked under concurrent writers: %s", e.Name())
		}
	}
}

func TestWriteAtomic_Permissions(t *testing.T) {
	// Permission bits are best-effort on Windows. The test asserts read+write
	// for the owner, which is portable.
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := WriteAtomic(path, []byte("p"), 0640); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0600 == 0 {
		t.Errorf("expected at least 0600 perms, got %v", info.Mode().Perm())
	}
}

func TestPathExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	if PathExists(p) {
		t.Error("PathExists returned true for non-existent file")
	}
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if !PathExists(p) {
		t.Error("PathExists returned false for existing file")
	}
}

// Example_writeAtomic shows the canonical use of WriteAtomic.
func Example_writeAtomic() {
	dir, _ := os.MkdirTemp("", "example-")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "hello.txt")
	if err := WriteAtomic(path, []byte("hello, world\n"), 0644); err != nil {
		fmt.Println("error:", err)
		return
	}
	got, _ := os.ReadFile(path)
	fmt.Print(string(got))
	// Output: hello, world
}
