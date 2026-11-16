package fileops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// hardLinkedPair creates outside/orig.txt and a hard link ws/hard.txt to it.
func hardLinkedPair(t *testing.T, body string) (outside, linked string) {
	t.Helper()
	dir := t.TempDir()
	outside = filepath.Join(dir, "orig.txt")
	linked = filepath.Join(dir, "ws-hard.txt")
	if err := os.WriteFile(outside, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, linked); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	return outside, linked
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("%s = %q, want %q", filepath.Base(path), b, want)
	}
}

func TestWriteFileBreaksHardLink(t *testing.T) {
	outside, linked := hardLinkedPair(t, "original\n")
	if _, err := WriteFile(linked, "new\n"); err != nil {
		t.Fatal(err)
	}
	assertContent(t, linked, "new\n")
	assertContent(t, outside, "original\n")
}

func TestPatchFileBreaksHardLink(t *testing.T) {
	outside, linked := hardLinkedPair(t, "alpha\nbeta\n")
	if _, err := PatchFile(linked, []PatchChange{{Old: "beta", New: "gamma"}}, ""); err != nil {
		t.Fatal(err)
	}
	assertContent(t, linked, "alpha\ngamma\n")
	assertContent(t, outside, "alpha\nbeta\n")
}

func TestUndoBreaksHardLink(t *testing.T) {
	outside, linked := hardLinkedPair(t, "after\n")
	tr := NewTracker(10)
	tr.Record(linked, "write", "before\n", "after\n")
	if _, err := tr.Undo(1); err != nil {
		t.Fatal(err)
	}
	assertContent(t, linked, "before\n")
	assertContent(t, outside, "after\n")
}

func TestReplaceFileContentsKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "script.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFileContents(path, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o750 {
		t.Fatalf("mode = %v, want 0750", st.Mode().Perm())
	}
}

func TestReplaceFileContentsFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := ReplaceFileContents(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertContent(t, target, "new")
	if li, err := os.Lstat(link); err != nil || li.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink itself was replaced (%v, %v)", li, err)
	}
	// Dangling link: the target is created through it.
	dangling := filepath.Join(dir, "dangling.txt")
	created := filepath.Join(dir, "created.txt")
	if err := os.Symlink(created, dangling); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFileContents(dangling, []byte("made"), 0o644); err != nil {
		t.Fatal(err)
	}
	assertContent(t, created, "made")
}

func TestReplaceFileContentsLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	for _, body := range []string{"one", "two", "three"} {
		if err := ReplaceFileContents(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries, want only a.txt", len(entries))
	}
	assertContent(t, path, "three")
}
