package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileAtomicCreatesParentsAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "f.json")
	if err := WriteFileAtomic(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "hello" {
		t.Errorf("got %q, %v", b, err)
	}
}

func TestWriteFileAtomicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions not supported on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "new", "f")
	if err := WriteFileAtomic(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %o", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("created dir mode = %o, want 700", di.Mode().Perm())
	}
}

func TestWriteFileAtomicOverwritesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	for _, content := range []string{"first, longer content", "second"} {
		if err := WriteFileAtomic(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(path); string(b) != content {
			t.Errorf("got %q, want %q", b, content)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d entries in dir, want 1", len(entries))
	}
}

func TestWriteFileAtomicEmptyAndLarge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := WriteFileAtomic(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 10<<20)
	if err := WriteFileAtomic(path, big, 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Size() != int64(len(big)) {
		t.Errorf("size = %d", fi.Size())
	}
}

func TestWriteFileAtomicFailsWhenParentIsAFile(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(filepath.Join(blocker, "f"), []byte("x"), 0o600); err == nil {
		t.Error("expected error")
	}
}
