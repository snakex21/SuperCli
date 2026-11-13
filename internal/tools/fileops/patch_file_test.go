package fileops

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchFile_SingleReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchFile(path, []PatchChange{{Old: "world", New: "there"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 1 || !res.Changed {
		t.Fatalf("got %+v", res)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello there\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestPatchFile_MultilineAndMultiChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.txt")
	orig := "line1\nline2\nline3\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchFile(path, []PatchChange{
		{Old: "line1\nline2", New: "A\nB"},
		{Old: "line3", New: "C"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 2 || !res.Changed {
		t.Fatalf("got %+v", res)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "A\nB\nC\n" {
		t.Fatalf("content = %q", got)
	}
}

func TestPatchFile_ExpectedCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "e.txt")
	if err := os.WriteFile(path, []byte("xx yy xx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// default expected_count=1 should fail when two matches
	if _, err := PatchFile(path, []PatchChange{{Old: "xx", New: "zz"}}, ""); err == nil {
		t.Fatal("expected error for expected_count=1 with 2 matches")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "xx yy xx\n" {
		t.Fatalf("file changed on error: %q", data)
	}
	res, err := PatchFile(path, []PatchChange{{Old: "xx", New: "zz", ExpectedCount: 2}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 2 {
		t.Fatalf("replacements=%d", res.Replacements)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "zz yy zz\n" {
		t.Fatalf("content=%q", got)
	}
}

func TestPatchFile_SecondChangeFailsLeavesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	orig := "aaa bbb ccc\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PatchFile(path, []PatchChange{
		{Old: "aaa", New: "AAA"},
		{Old: "nope", New: "x"},
	}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != orig {
		t.Fatalf("partial write: %q", got)
	}
}

func TestPatchFile_BaseHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.txt")
	content := []byte("stable\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])
	// A hash this tool never issued for the file is a guess: it is ignored
	// with a note, never reported as "file changed".
	res, err := PatchFile(path, []PatchChange{{Old: "stable", New: "ok"}}, "deadbeef")
	if err != nil || !res.Changed || res.BeforeHash != good || !strings.Contains(res.Note, "base_hash ignored") {
		t.Fatalf("invented hash: %+v %v", res, err)
	}
	// An issued hash that still matches is accepted silently.
	res2, err := PatchFile(path, []PatchChange{{Old: "ok", New: "fine"}}, strings.ToUpper(res.AfterHash))
	if err != nil || !res2.Changed || res2.Note != "" {
		t.Fatalf("issued hash: %+v %v", res2, err)
	}
	// An issued hash after someone else wrote the file is a real stale edit.
	if err := os.WriteFile(path, []byte("fine\nexternal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = PatchFile(path, []PatchChange{{Old: "fine", New: "x"}}, res2.AfterHash)
	if err == nil || !strings.Contains(err.Error(), "file changed since the result that gave this base_hash") {
		t.Fatalf("stale issued hash accepted: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "fine\nexternal\n" {
		t.Fatalf("stale edit wrote: %q", got)
	}
}

func TestPatchFile_NoopChangedFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "n.txt")
	if err := os.WriteFile(path, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchFile(path, []PatchChange{{Old: "same", New: "same"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.Replacements != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestPatchFile_Missing(t *testing.T) {
	_, err := PatchFile(filepath.Join(t.TempDir(), "nope.txt"), []PatchChange{{Old: "a", New: "b"}}, "")
	if err == nil || !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("err=%v", err)
	}
}

func TestCreateFileExclusive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "new.txt")
	if err := CreateFileExclusive(path, "hi"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hi" {
		t.Fatalf("%q", got)
	}
	if err := CreateFileExclusive(path, "other"); err == nil {
		t.Fatal("overwrite should fail")
	}
	got, _ = os.ReadFile(path)
	if string(got) != "hi" {
		t.Fatalf("content changed: %q", got)
	}
}
