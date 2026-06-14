package fileops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_CreatesNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	res, err := WriteFile(path, "hello")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !res.Created {
		t.Error("Created = false, want true for a new file")
	}
	if res.Bytes != 5 {
		t.Errorf("Bytes = %d, want 5", res.Bytes)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestWriteFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := WriteFile(path, "new content")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if res.Created {
		t.Error("Created = true, want false for an overwrite")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new content" {
		t.Errorf("content = %q, want 'new content'", got)
	}
}

func TestWriteFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "deep.txt")
	res, err := WriteFile(path, "deep")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !res.Created {
		t.Error("Created = false, want true")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("nested file not created: %v", err)
	}
}

func TestWriteFile_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	res, err := WriteFile(path, "")
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if res.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0", res.Bytes)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("empty file not created: %v", err)
	}
}

func TestWriteFile_UTF8Preserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "utf8.txt")
	const content = "zażółć gęślą jaźń — π≈3.14"
	res, err := WriteFile(path, content)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("UTF-8 content not preserved: %q", got)
	}
	if res.Bytes != len(content) {
		t.Errorf("Bytes = %d, want %d", res.Bytes, len(content))
	}
}

func TestWriteFile_EmptyPathErrors(t *testing.T) {
	if _, err := WriteFile("", "x"); err == nil {
		t.Fatal("want error for empty path")
	}
}
