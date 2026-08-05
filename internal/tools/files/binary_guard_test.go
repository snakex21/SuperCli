package files

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGuardDocx creates a real .docx (a zip with [Content_Types].xml
// and word/document.xml, like the office package fixtures) so these
// tests exercise the exact file shape that was being destroyed in
// production: a text tool pointed at a Word document.
func writeGuardDocx(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, part := range []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`},
		{"word/document.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>Raport kwartalny</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Przychód wyniósł 3000 zł.</w:t></w:r></w:p>` +
			`</w:body></w:document>`},
	} {
		fw, err := w.Create(part.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(part.body)); err != nil {
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

func guardHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// assertDocxIntact fails when the document's bytes changed or it no
// longer opens as a zip — i.e. exactly the damage Word reports as
// "unreadable content".
func assertDocxIntact(t *testing.T, path, beforeHash string) {
	t.Helper()
	if after := guardHash(t, path); after != beforeHash {
		t.Fatalf("document was modified: %s -> %s", beforeHash[:12], after[:12])
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("document no longer opens as a zip: %v", err)
	}
	zr.Close()
}

func TestWriteFile_RefusesDocxAndLeavesItIntact(t *testing.T) {
	dir := t.TempDir()
	path := writeGuardDocx(t, dir, "raport.docx")
	before := guardHash(t, path)

	res := runWriteFile(t, NewWriteFile(dir), `{"path":"raport.docx","content":"Raport kwartalny\nPrzychod wyniosl 4000 zl.\n"}`)
	if res.Err == nil {
		t.Fatalf("write_file overwrote a Word document: %q", res.Text)
	}
	msg := res.Err.Error()
	for _, want := range []string{"raport.docx", "Word document", "edit_docx"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	assertDocxIntact(t, path, before)
}

func TestReadLines_RefusesDocxInsteadOfStreamingReplacementChars(t *testing.T) {
	dir := t.TempDir()
	writeGuardDocx(t, dir, "raport.docx")

	tool := NewReadLines(dir)
	res, err := tool.execute(context.Background(), json.RawMessage(`{"file":"raport.docx","from":1,"to":20}`))
	if err != nil {
		t.Fatalf("read_lines go-error: %v", err)
	}
	if res.Err == nil {
		t.Fatalf("read_lines returned document bytes as text: %q", res.Text)
	}
	if strings.ContainsRune(res.Text, '�') {
		t.Errorf("output still contains U+FFFD: %q", res.Text)
	}
	if !strings.Contains(res.Err.Error(), "read_docx") {
		t.Errorf("error %q does not point at read_docx", res.Err)
	}
}

func TestPatchFile_RefusesDocxWithADiagnosticError(t *testing.T) {
	dir := t.TempDir()
	path := writeGuardDocx(t, dir, "raport.docx")
	before := guardHash(t, path)

	tool := NewPatchFile(dir)
	// The text IS in the document, but compressed. Before the guard this
	// reported "found 0", which is what pushed the model to write_file.
	res, err := tool.execute(context.Background(), json.RawMessage(`{"path":"raport.docx","changes":[{"old":"3000","new":"4000"}]}`))
	if err != nil {
		t.Fatalf("patch_file go-error: %v", err)
	}
	if res.Err == nil {
		t.Fatalf("patch_file patched a Word document: %q", res.Text)
	}
	if strings.Contains(res.Err.Error(), "found 0") {
		t.Errorf("error still claims the text is absent: %v", res.Err)
	}
	if !strings.Contains(res.Err.Error(), "edit_docx") {
		t.Errorf("error %q does not point at edit_docx", res.Err)
	}
	assertDocxIntact(t, path, before)
}

// A binary file with no extension at all: only the content check can
// catch it, and it must.
func TestTextTools_RefuseExtensionlessBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload")
	if err := os.WriteFile(path, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	before := guardHash(t, path)

	res := runWriteFile(t, NewWriteFile(dir), `{"path":"payload","content":"text"}`)
	if res.Err == nil {
		t.Fatal("write_file overwrote an extensionless binary file")
	}
	if !strings.Contains(res.Err.Error(), "binary file") {
		t.Errorf("error %q should name it a binary file", res.Err)
	}
	if after := guardHash(t, path); after != before {
		t.Error("extensionless binary file was modified")
	}

	read := NewReadLines(dir)
	rres, err := read.execute(context.Background(), json.RawMessage(`{"file":"payload","from":1,"to":5}`))
	if err != nil {
		t.Fatalf("read_lines go-error: %v", err)
	}
	if rres.Err == nil {
		t.Fatalf("read_lines returned binary bytes: %q", rres.Text)
	}
}

// Regression: the full tool set on ordinary text files, including an
// empty one and a Polish-language one, must behave exactly as before.
func TestTextTools_RegressionOnTextFiles(t *testing.T) {
	for _, name := range []string{"main.go", "README.md", "notes.txt", "data.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			body := "Zażółć gęślą jaźń\ndruga linia\ntrzecia linia\n"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			readRes, err := NewReadLines(dir).execute(context.Background(), json.RawMessage(`{"file":"`+name+`","from":1,"to":3}`))
			if err != nil {
				t.Fatal(err)
			}
			if readRes.Err != nil {
				t.Fatalf("read_lines: %v", readRes.Err)
			}
			if !strings.Contains(readRes.Text, "Zażółć gęślą jaźń") {
				t.Errorf("Polish text mangled: %q", readRes.Text)
			}
			editRes, err := NewPatchFile(dir).execute(context.Background(),
				json.RawMessage(`{"path":"`+name+`","old":"druga linia","new":"DRUGA"}`))
			if err != nil {
				t.Fatal(err)
			}
			if editRes.Err != nil {
				t.Fatalf("patch_file shorthand: %v", editRes.Err)
			}
			patchRes, err := NewPatchFile(dir).execute(context.Background(),
				json.RawMessage(`{"path":"`+name+`","changes":[{"old":"trzecia","new":"TRZECIA"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			if patchRes.Err != nil {
				t.Fatalf("patch_file: %v", patchRes.Err)
			}
			writeRes := runWriteFile(t, NewWriteFile(dir), `{"path":"`+name+`","content":"świeża treść\n"}`)
			if writeRes.Err != nil {
				t.Fatalf("write_file: %v", writeRes.Err)
			}
			got, _ := os.ReadFile(filepath.Join(dir, name))
			if string(got) != "świeża treść\n" {
				t.Errorf("content = %q", got)
			}
		})
	}
}

func TestTextTools_EmptyAndTinyFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runWriteFile(t, NewWriteFile(dir), `{"path":"empty.txt","content":"x"}`); res.Err != nil {
		t.Fatalf("write_file on empty file: %v", res.Err)
	}
	if res := runWriteFile(t, NewWriteFile(dir), `{"path":"brand_new.txt","content":"y"}`); res.Err != nil {
		t.Fatalf("write_file creating a new file: %v", res.Err)
	}
}
