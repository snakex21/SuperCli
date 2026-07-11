package fileops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FileErr maps raw OS errors to the short deterministic forms
// the model sees: not_found / permission / is_directory + path.

func TestFileErr_Nil(t *testing.T) {
	if got := FileErr(nil, "x"); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestFileErr_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := os.ReadFile(path)
	got := FileErr(err, path)
	if got.Error() != "not_found "+path {
		t.Errorf("got %q", got)
	}
}

func TestFileErr_IsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := os.ReadFile(dir)
	if err == nil {
		t.Skip("reading a directory succeeded on this platform")
	}
	got := FileErr(err, dir)
	if got.Error() != "is_directory "+dir {
		t.Errorf("got %q", got)
	}
}

func TestFileErr_UnknownPassesThrough(t *testing.T) {
	orig := errors.New("some exotic failure")
	path := filepath.Join(t.TempDir(), "gone.bin") // does not exist -> no is_directory rewrite
	if got := FileErr(orig, path); got != orig {
		t.Errorf("got %v, want passthrough", got)
	}
}

func TestReadLines_MissingFile_StructuredError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ReadLines(path, 1, 5)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.HasPrefix(err.Error(), "not_found ") {
		t.Errorf("err = %q, want not_found prefix", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %q, want path", err)
	}
}

func TestMove_MissingSource_StructuredError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "absent.txt")
	_, err := Move(src, filepath.Join(dir, "dst.txt"))
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.HasPrefix(err.Error(), "not_found ") {
		t.Errorf("err = %q, want not_found prefix", err)
	}
}
