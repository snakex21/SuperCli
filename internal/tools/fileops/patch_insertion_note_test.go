package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A one-off patch must stay silent: the success path pays tokens only when the
// note is genuinely diagnostic.
func TestPatchFile_OrdinaryPatchHasNoNote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.js")
	if err := os.WriteFile(path, []byte("const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchFile(path, []PatchChange{
		{Old: "const x = 1;", New: "const x = 1;\n// helper for the modal"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" || res.Duplicated {
		t.Fatalf("first insertion should be silent, got %+v", res)
	}
	// A plain replacement is not an insertion at all, however often the text
	// appears elsewhere.
	if err := os.WriteFile(path, []byte("aaa bbb aaa bbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = PatchFile(path, []PatchChange{{Old: "aaa bbb aaa", New: "zzz"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" || res.Duplicated {
		t.Fatalf("replacement is not an insertion, got %+v", res)
	}
}

// Re-applying the same append is what the model could not see. Say it.
func TestPatchFile_DuplicateInsertionIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scripts.js")
	if err := os.WriteFile(path, []byte("const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	change := []PatchChange{{Old: "const x = 1;", New: "const x = 1;\n// helper for the modal"}}
	for i := 2; i <= 4; i++ {
		if _, err := PatchFile(path, change, ""); err != nil {
			t.Fatal(err)
		}
		res, err := PatchFile(path, change, "")
		if err != nil {
			t.Fatal(err)
		}
		_ = res
	}
	res, err := PatchFile(path, change, "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Duplicated {
		t.Fatal("a re-applied insertion must be flagged as duplicated")
	}
	if !strings.HasPrefix(res.Note, "note: pure insertion; the inserted text now occurs ") {
		t.Fatalf("note = %q", res.Note)
	}
	if !strings.HasSuffix(res.Note, " times in this file") {
		t.Fatalf("note = %q", res.Note)
	}
	want := strings.Count(mustRead(t, path), "// helper for the modal")
	if !strings.Contains(res.Note, " occurs "+itoa(want)+" times") {
		t.Fatalf("note %q does not match the %d real occurrences", res.Note, want)
	}
}

// A short tail (a newline, a brace, a semicolon) occurs everywhere in every
// file. Counting it would turn the success path into noise.
func TestPatchFile_ShortInsertionIsNotWorthANote(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.js")
	if err := os.WriteFile(path, []byte("a;\nb;\nc;\nd;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := PatchFile(path, []PatchChange{{Old: "a;", New: "a;\nb;"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Note != "" || res.Duplicated {
		t.Fatalf("short tail must not produce a note, got %+v", res)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
