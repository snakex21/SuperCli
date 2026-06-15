package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runEditDocx(t *testing.T, tool *EditDocxTool, args string) Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit_docx error: %v", err)
	}
	return res
}

func readDocxText(t *testing.T, dir, name string) string {
	t.Helper()
	rd := NewReadDocx(dir, 0)
	res, err := rd.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q}`, name)))
	if err != nil {
		t.Fatalf("read_docx error: %v", err)
	}
	return res.Text
}

func TestEditDocx_ReplaceSimple(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Hello world.", "Goodbye world."})
	tool := NewEditDocx(dir)

	res := runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"world","replace":"team"}`)
	if !strings.Contains(res.Text, "Replaced 2 occurrence(s)") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	got := readDocxText(t, dir, "a.docx")
	if !strings.Contains(got, "Hello team.") || !strings.Contains(got, "Goodbye team.") {
		t.Fatalf("round-trip text wrong: %q", got)
	}
	// Backup must exist with the ORIGINAL content.
	bak := filepath.Join(dir, "a.docx.bak")
	if _, err := os.Stat(bak); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if got := readDocxText(t, dir, "a.docx.bak"); !strings.Contains(got, "Hello world.") {
		t.Fatalf("backup content wrong: %q", got)
	}
}

// Text split across multiple runs in one paragraph must still
// be found (the run-merge behavior).
func TestEditDocx_ReplaceAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "split.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	fw, _ := w.Create("[Content_Types].xml")
	fw.Write([]byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`))
	fw, _ = w.Create("word/document.xml")
	// "magic word" split as "mag" + "ic wo" + "rd", second run bold.
	fw.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="` + wordProcessingNS + `"><w:body>` +
		`<w:p><w:r><w:t>mag</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>ic wo</w:t></w:r><w:r><w:t>rd here</w:t></w:r></w:p>` +
		`</w:body></w:document>`))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"split.docx","action":"replace","find":"magic word","replace":"plain text"}`)
	if !strings.Contains(res.Text, "Replaced 1 occurrence(s)") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
	got := readDocxText(t, dir, "split.docx")
	if !strings.Contains(got, "plain text here") {
		t.Fatalf("cross-run replace failed: %q", got)
	}
}

func TestEditDocx_ReplaceNoMatchNoBackup(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Hello."})
	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"zzz","replace":"y"}`)
	if !strings.Contains(res.Text, "No occurrences") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.docx.bak")); err == nil {
		t.Fatal("backup should not be created when nothing changed")
	}
}

// Untouched zip entries must survive byte-for-byte.
func TestEditDocx_PreservesOtherEntries(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "a.docx", []string{"Hello world."})

	// Append a custom entry to the archive to track.
	// Rebuild zip with an extra entry.
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(strings.NewReader(string(orig)), int64(len(orig)))
	if err != nil {
		t.Fatal(err)
	}
	f2, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f2)
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		w, _ := zw.Create(zf.Name)
		buf := make([]byte, zf.UncompressedSize64)
		if _, err := io.ReadFull(rc, buf); err != nil {
			t.Fatal(err)
		}
		w.Write(buf)
		rc.Close()
	}
	marker := []byte("custom-marker-payload-12345")
	w, _ := zw.Create("docProps/custom.xml")
	w.Write(marker)
	zw.Close()
	f2.Close()

	tool := NewEditDocx(dir)
	runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"world","replace":"team"}`)

	got, err := readZipEntry(path, "docProps/custom.xml", 1<<20)
	if err != nil {
		t.Fatalf("custom entry lost: %v", err)
	}
	if string(got) != string(marker) {
		t.Fatalf("custom entry corrupted: %q", got)
	}
}

func TestEditDocx_Append(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Intro."})
	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"a.docx","action":"append","text":"# Summary\nAll done.","style":""}`)
	if !strings.Contains(res.Text, "Appended 2 paragraph(s)") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	got := readDocxText(t, dir, "a.docx")
	want := []string{"Intro.", "Summary", "All done."}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in %q", w, got)
		}
	}
	// Order: Intro before Summary.
	if strings.Index(got, "Intro.") > strings.Index(got, "Summary") {
		t.Fatalf("appended text not at end: %q", got)
	}
}

func TestEditDocx_CreateAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditDocx(dir)
	res := runEditDocx(t, tool, `{"path":"new.docx","action":"create","text":"# Title\nFirst paragraph.\n## Section\n**Bold note**"}`)
	if !strings.Contains(res.Text, "Created") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	got := readDocxText(t, dir, "new.docx")
	for _, w := range []string{"Title", "First paragraph.", "Section", "Bold note"} {
		if !strings.Contains(got, w) {
			t.Fatalf("missing %q in round-trip read: %q", w, got)
		}
	}
}

func TestEditDocx_CreateRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"Existing."})
	tool := NewEditDocx(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx","action":"create","text":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want overwrite refusal, got %v", err)
	}
}

func TestEditDocx_SandboxEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditDocx(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"../outside.docx","action":"create","text":"x"}`))
	if err == nil {
		t.Fatal("want sandbox error for path escape")
	}
}

func TestEditDocx_SpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{"A & B < C."})
	tool := NewEditDocx(dir)
	runEditDocx(t, tool, `{"path":"a.docx","action":"replace","find":"A & B","replace":"X <> \"Y\""}`)
	got := readDocxText(t, dir, "a.docx")
	if !strings.Contains(got, `X <> "Y" < C.`) {
		t.Fatalf("escaping broken: %q", got)
	}
}

func TestEditDocx_Spec(t *testing.T) {
	spec := NewEditDocx(".").Spec()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Name != "edit_docx" {
		t.Fatalf("name: %q", spec.Name)
	}
	if !strings.Contains(spec.Description, "LIMITATION") {
		t.Error("description must document the run-normalization limitation")
	}
}
