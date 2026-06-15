package office

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeTestPdf writes a minimal valid 1-page
// PDF at <dir>/<name> with a single line of
// text. The PDF is built dynamically so the
// xref table offsets are correct — ledongthuc/pdf
// (and any other strict parser) refuses a
// PDF with a wrong xref.
//
// If you want more pages, duplicate the page
// + contents objects and add them to the
// /Pages /Kids array.
func writeTestPdf(t *testing.T, dir, name string) string {
	t.Helper()
	return writeTestPdfPages(t, dir, name, "Hello, World!", 1)
}

func writeTestPdfPages(t *testing.T, dir, name, text string, pages int) string {
	t.Helper()
	if pages < 1 {
		pages = 1
	}

	// We build each object as a string, then
	// stitch them into the final file with
	// the header and xref at the right byte
	// offsets.
	type obj struct {
		body string
	}
	objs := make([]obj, 0, 4+pages*2)

	// 1: catalog
	objs = append(objs, obj{"<< /Type /Catalog /Pages 2 0 R >>"})

	// 2: pages — references page objects
	kids := make([]string, pages)
	for i := 0; i < pages; i++ {
		// Page objects start at index 3, 5, 7, ...
		kids[i] = strconv.Itoa(3+i*2) + " 0 R"
	}
	pagesBody := "<< /Type /Pages /Kids [" + strings.Join(kids, " ") + "] /Count " + strconv.Itoa(pages) + " >>"
	objs = append(objs, obj{pagesBody})

	// 3, 5, 7, ...: page objects
	// 4, 6, 8, ...: content stream objects
	// Font object comes after all pages.
	for i := 0; i < pages; i++ {
		contentObjNum := 4 + i*2
		fontObjNum := 4 + pages*2 - 1
		pageBody := "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Contents " + strconv.Itoa(contentObjNum) + " 0 R " +
			"/Resources << /Font << /F1 " + strconv.Itoa(fontObjNum) + " 0 R >> >> >>"
		objs = append(objs, obj{pageBody})

		// Content stream — Tj with our text on
		// line i (offset y by 20 per page so
		// pages are visually distinct).
		stream := "BT /F1 12 Tf 50 " + strconv.Itoa(750-i*20) + " Td (" + escapePdfText(text) + ") Tj ET"
		contentBody := "<< /Length " + strconv.Itoa(len(stream)) + " >>\nstream\n" + stream + "\nendstream"
		objs = append(objs, obj{contentBody})
	}

	// Last: font object
	objs = append(objs, obj{"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"})

	// Build the file.
	var file strings.Builder
	file.WriteString("%PDF-1.4\n")
	// Some parsers like a comment line with
	// binary markers in the header to set
	// the binary flag.
	file.WriteString("%\xE2\xE3\xCF\xD3\n")

	offsets := make([]int, len(objs)+1)
	offsets[0] = 0
	for i, o := range objs {
		offsets[i+1] = file.Len()
		file.WriteString(strconv.Itoa(i+1) + " 0 obj\n")
		file.WriteString(o.body)
		file.WriteString("\nendobj\n")
	}

	xrefOffset := file.Len()
	file.WriteString("xref\n")
	file.WriteString("0 " + strconv.Itoa(len(objs)+1) + "\n")
	file.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		file.WriteString(pad10(offsets[i]) + " 00000 n \n")
	}
	file.WriteString("trailer\n")
	file.WriteString("<< /Size " + strconv.Itoa(len(objs)+1) + " /Root 1 0 R >>\n")
	file.WriteString("startxref\n")
	file.WriteString(strconv.Itoa(xrefOffset) + "\n")
	file.WriteString("%%EOF\n")

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(file.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// pad10 left-pads a number to 10 digits with
// leading zeros — the xref table format
// requires exactly 10 digits per entry.
func pad10(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

// escapePdfText escapes PDF text-string
// characters: ( and ) and \.
func escapePdfText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}

func TestReadPdf_Basic(t *testing.T) {
	dir := t.TempDir()
	writeTestPdf(t, dir, "a.pdf")
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.pdf"}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "Hello, World!") {
		t.Errorf("missing 'Hello, World!' in:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "--- Page 1 ---") {
		t.Errorf("missing page header in:\n%s", res.Text)
	}
}

func TestReadPdf_MultiPage(t *testing.T) {
	dir := t.TempDir()
	writeTestPdfPages(t, dir, "two.pdf", "Hello, World!", 2)
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"two.pdf"}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if !strings.Contains(res.Text, "--- Page 1 ---") {
		t.Error("missing Page 1 header")
	}
	if !strings.Contains(res.Text, "--- Page 2 ---") {
		t.Error("missing Page 2 header")
	}
}

func TestReadPdf_MaxPagesCap(t *testing.T) {
	dir := t.TempDir()
	writeTestPdfPages(t, dir, "three.pdf", "Hello, World!", 3)
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"three.pdf","max_pages":2}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if strings.Contains(res.Text, "--- Page 3 ---") {
		t.Error("page 3 should have been skipped")
	}
	if !strings.Contains(res.Text, "--- Page 2 ---") {
		t.Error("page 2 should still be present")
	}
}

func TestReadPdf_NotAPdf(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("not a pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"plain.txt"}`))
	if res.Err == nil {
		t.Error("expected error for non-pdf file")
	}
}

func TestReadPdf_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"nope.pdf"}`))
	if res.Err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadPdf_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "path is required") {
		t.Errorf("expected 'path is required'; got %v", res.Err)
	}
}

func TestReadPdf_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	// Build a real (small) PDF, then make the
	// cap ridiculously small to force a cap.
	writeTestPdf(t, dir, "a.pdf")
	tool := NewReadPdf(dir, 100) // 100 bytes cap
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.pdf"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "too large") {
		t.Errorf("expected 'too large'; got %v", res.Err)
	}
}

func TestReadPdf_RegisteredInRegistry(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadPdf(dir, 0)
	r := NewRegistry()
	if err := r.Register(tool.Spec()); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Execute(context.Background(), "read_pdf", json.RawMessage(`{"path":"a.pdf"}`))
	if got.Err == nil {
		t.Error("expected missing-file error from registry Execute")
	}
}

func TestReadPdf_Spec(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadPdf(dir, 0)
	spec := tool.Spec()
	if spec.Name != "read_pdf" {
		t.Errorf("Name = %q, want read_pdf", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

func TestReadPdf_SpecialCharactersInText(t *testing.T) {
	dir := t.TempDir()
	// () and \ must be escaped in PDF text
	// strings. The helper handles that; here
	// we just verify the round-trip preserves
	// the text.
	writeTestPdfPages(t, dir, "a.pdf", `a (b) c \d`, 1)
	tool := NewReadPdf(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.pdf"}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if !strings.Contains(res.Text, `a (b) c \d`) {
		t.Errorf("special chars not preserved; got %q", res.Text)
	}
}
