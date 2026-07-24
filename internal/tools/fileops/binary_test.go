package fileops

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalDocx creates a real .docx at dir/name: a zip holding
// [Content_Types].xml and word/document.xml, exactly the shape the
// office package's own fixtures use. Any text tool applied to it
// corrupts it, which is the point of these tests.
func writeMinimalDocx(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`,
		"word/document.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Umowa najmu lokalu użytkowego</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Strony ustalają czynsz w wysokości 3000 zł.</w:t></w:r></w:p>` +
			`</w:body></w:document>`,
	}
	// Written in a fixed order so the archive bytes are reproducible.
	for _, part := range []string{"[Content_Types].xml", "word/document.xml"} {
		fw, err := w.Create(part)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(parts[part])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// --- detector unit tests ---

func TestLooksBinary_Content(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", nil, false},
		{"ascii", []byte("package main\n"), false},
		{"polish utf8", []byte("Zażółć gęślą jaźń — ćma, łódź, ŚĆĘ\n"), false},
		{"emoji", []byte("done ✅ 🚀\n"), false},
		{"nul byte", []byte{'a', 0x00, 'b'}, true},
		{"invalid utf8", []byte{0xff, 0xfe, 0x41, 0x42}, true},
		{"zip header", []byte("PK\x03\x04\x14\x00\x06\x00"), true},
	}
	for _, tc := range cases {
		if got := looksBinary(tc.data, false); got != tc.want {
			t.Errorf("%s: looksBinary = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A multi-byte rune cut in half by the 8 KB sniff window must not be
// read as binary garbage — the user works in Polish.
func TestLooksBinary_TruncatedRuneIsNotBinary(t *testing.T) {
	full := []byte("ż")
	head := append([]byte("tekst "), full[0]) // dangling lead byte
	if looksBinary(head, true) {
		t.Error("truncated rune flagged as binary")
	}
	if !looksBinary(head, false) {
		t.Error("a complete file ending in a dangling lead byte is invalid UTF-8")
	}
}

func TestEnsureTextFile_DocxIsRefusedWithDocxAdvice(t *testing.T) {
	dir := t.TempDir()
	path := writeMinimalDocx(t, dir, "umowa.docx")
	err := EnsureTextFile(path)
	if err == nil {
		t.Fatal("EnsureTextFile accepted a .docx")
	}
	msg := err.Error()
	for _, want := range []string{"umowa.docx", "Word document", "edit_docx", "read_docx"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

// Content outranks the name: a binary blob with no extension at all is
// still caught, because the bytes cannot lie.
func TestEnsureTextFile_ExtensionlessBinaryCaughtByContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob")
	if err := os.WriteFile(path, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	err := EnsureTextFile(path)
	if err == nil {
		t.Fatal("extensionless NUL blob accepted as text")
	}
	if !strings.Contains(err.Error(), "binary file") {
		t.Errorf("message %q should name it a binary file", err)
	}
}

// The mirror image: bytes outrank the name in the permissive direction
// too, so a plain-text file that merely happens to end in .docx stays
// editable instead of producing a false refusal.
func TestEnsureTextFile_TextContentBeatsBinaryExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.docx")
	if err := os.WriteFile(path, []byte("to jest zwykły tekst\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTextFile(path); err != nil {
		t.Errorf("text file refused: %v", err)
	}
}

// The one case content cannot answer: the file does not exist yet, and
// write_file is about to create "raport.docx" out of plain text.
func TestEnsureTextFile_MissingPathFallsBackToExtension(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureTextFile(filepath.Join(dir, "raport.docx")); err == nil {
		t.Error("creating a .docx from text was accepted")
	}
	if err := EnsureTextFile(filepath.Join(dir, "raport.md")); err != nil {
		t.Errorf("creating a .md was refused: %v", err)
	}
}

func TestEnsureTextFile_EmptyAndTinyFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tiny := filepath.Join(dir, "tiny.txt")
	if err := os.WriteFile(tiny, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{empty, tiny} {
		if err := EnsureTextFile(p); err != nil {
			t.Errorf("%s refused: %v", filepath.Base(p), err)
		}
	}
}

// --- guarded operations leave the document byte-identical ---

func TestFileops_TextToolsRefuseDocxAndLeaveItIntact(t *testing.T) {
	ops := []struct {
		name string
		run  func(path string) error
	}{
		{"WriteFile", func(p string) error {
			_, err := WriteFile(p, "Nowa treść umowy")
			return err
		}},
		{"PatchFile", func(p string) error {
			_, err := PatchFile(p, []PatchChange{{Old: "3000", New: "4000"}}, "")
			return err
		}},
		{"EditLine", func(p string) error {
			_, err := EditLine(p, 1, "zmiana")
			return err
		}},
		{"EditLineAnchored", func(p string) error {
			_, err := EditLineAnchored(p, 1, "PK", "zmiana")
			return err
		}},
		{"EditLinesAnchored", func(p string) error {
			_, err := EditLinesAnchored(p, []AnchoredEdit{{Line: 1, ExpectedOld: "PK", NewContent: "zmiana"}})
			return err
		}},
		{"InsertAfter", func(p string) error {
			_, err := InsertAfter(p, 1, "dopisek")
			return err
		}},
		{"DeleteLines", func(p string) error {
			_, err := DeleteLines(p, 1, 1)
			return err
		}},
		{"ReadLines", func(p string) error {
			_, err := ReadLines(p, 1, 5)
			return err
		}},
		{"ReadContext", func(p string) error {
			_, err := ReadContext(p, 1, 3)
			return err
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeMinimalDocx(t, dir, "umowa.docx")
			before := hashFile(t, path)
			err := op.run(path)
			if err == nil {
				t.Fatalf("%s accepted a .docx", op.name)
			}
			if !strings.Contains(err.Error(), "edit_docx") {
				t.Errorf("%s error %q does not point at edit_docx", op.name, err)
			}
			if after := hashFile(t, path); after != before {
				t.Errorf("%s modified the document (%s -> %s)", op.name, before[:12], after[:12])
			}
			// The document must still open as a zip.
			zr, zerr := zip.OpenReader(path)
			if zerr != nil {
				t.Fatalf("%s corrupted the zip: %v", op.name, zerr)
			}
			zr.Close()
		})
	}
}

// --- regression: ordinary text files keep working ---

func TestFileops_TextFilesStillWork(t *testing.T) {
	for _, name := range []string{"main.go", "README.md", "notes.txt", "data.json", "noext"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadLines(path, 1, 3); err != nil {
				t.Fatalf("ReadLines: %v", err)
			}
			if _, err := ReadContext(path, 2, 1); err != nil {
				t.Fatalf("ReadContext: %v", err)
			}
			if _, err := EditLine(path, 1, "ALPHA"); err != nil {
				t.Fatalf("EditLine: %v", err)
			}
			if _, err := EditLineAnchored(path, 2, "beta", "BETA"); err != nil {
				t.Fatalf("EditLineAnchored: %v", err)
			}
			if _, err := InsertAfter(path, 1, "inserted"); err != nil {
				t.Fatalf("InsertAfter: %v", err)
			}
			if _, err := DeleteLines(path, 1, 1); err != nil {
				t.Fatalf("DeleteLines: %v", err)
			}
			if _, err := PatchFile(path, []PatchChange{{Old: "BETA", New: "delta"}}, ""); err != nil {
				t.Fatalf("PatchFile: %v", err)
			}
			if _, err := WriteFile(path, "fresh\n"); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != "fresh\n" {
				t.Errorf("content = %q", got)
			}
		})
	}
}

// The user works in Polish: diacritics must never look like binary,
// including in a file large enough to exercise the sniff window.
func TestFileops_PolishTextIsNotBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notatki.txt")
	var b strings.Builder
	for b.Len() < 3*binarySniffBytes {
		b.WriteString("Zażółć gęślą jaźń — ćwierć, łódź, ŹRÓDŁO, ŚCIEŻKA, ĄĘŃÓŚ\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureTextFile(path); err != nil {
		t.Fatalf("Polish text refused: %v", err)
	}
	if _, err := ReadLines(path, 1, 5); err != nil {
		t.Fatalf("ReadLines: %v", err)
	}
	unique := "Wyjątkowa linia: zażółć\n"
	if err := os.WriteFile(path, []byte(b.String()+unique), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PatchFile(path, []PatchChange{{Old: "Wyjątkowa", New: "Zwykła"}}, ""); err != nil {
		t.Fatalf("PatchFile: %v", err)
	}
}
