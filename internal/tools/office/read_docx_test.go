package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestDocx creates a minimal valid .docx
// file at <dir>/<name> with the given paragraphs
// and (optionally) one simple table. The output
// is a real zip with [Content_Types].xml and
// word/document.xml — just enough to be parsed
// by ReadDocxTool.
func writeTestDocx(t *testing.T, dir, name string, paragraphs []string) string {
	t.Helper()
	return writeTestDocxFull(t, dir, name, paragraphs, nil)
}

type docxTableSpec struct {
	Rows [][]string
}

// writeTestDocxFull writes a docx with
// paragraphs followed by a single table (if
// non-nil). The table is a simple grid: rows
// of cell-text values.
func writeTestDocxFull(t *testing.T, dir, name string, paragraphs []string, table *docxTableSpec) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)

	// [Content_Types].xml
	const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	if fw, err := w.Create("[Content_Types].xml"); err != nil {
		t.Fatal(err)
	} else if _, err := fw.Write([]byte(contentTypes)); err != nil {
		t.Fatal(err)
	}

	// word/document.xml
	var doc strings.Builder
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	doc.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, p := range paragraphs {
		doc.WriteString(`<w:p><w:r><w:t>`)
		xml.EscapeText(&doc, []byte(p))
		doc.WriteString(`</w:t></w:r></w:p>`)
	}
	if table != nil {
		doc.WriteString(`<w:tbl>`)
		for _, row := range table.Rows {
			doc.WriteString(`<w:tr>`)
			for _, cell := range row {
				doc.WriteString(`<w:tc><w:p><w:r><w:t>`)
				xml.EscapeText(&doc, []byte(cell))
				doc.WriteString(`</w:t></w:r></w:p></w:tc>`)
			}
			doc.WriteString(`</w:tr>`)
		}
		doc.WriteString(`</w:tbl>`)
	}
	doc.WriteString(`</w:body></w:document>`)
	if fw, err := w.Create("word/document.xml"); err != nil {
		t.Fatal(err)
	} else if _, err := fw.Write([]byte(doc.String())); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadDocx_BasicParagraphs(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{
		"First paragraph.",
		"Second paragraph.",
		"Third paragraph.",
	})
	tool := NewReadDocx(dir, 0)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	want := "First paragraph.\nSecond paragraph.\nThird paragraph.\n"
	if res.Text != want {
		t.Errorf("text mismatch\n--- got ---\n%s\n--- want ---\n%s", res.Text, want)
	}
}

func TestReadDocx_MultipleRunsInOneParagraph(t *testing.T) {
	dir := t.TempDir()
	// Build a custom docx with a paragraph that
	// has three runs (different styles, in
	// practice — the renderer must concatenate
	// them in order).
	path := filepath.Join(dir, "a.docx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Create("word/_rels/document.xml.rels")
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p>
  <w:r><w:t xml:space="preserve">Hello </w:t></w:r>
  <w:r><w:t>brave </w:t></w:r>
  <w:r><w:t>world.</w:t></w:r>
</w:p>
</w:body></w:document>`
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(doc))
	w.Close()
	f.Close()

	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx"}`))
	if !strings.Contains(res.Text, "Hello brave world.") {
		t.Errorf("runs not concatenated; got %q", res.Text)
	}
}

func TestReadDocx_Table(t *testing.T) {
	dir := t.TempDir()
	writeTestDocxFull(t, dir, "a.docx", []string{"Header:"}, &docxTableSpec{
		Rows: [][]string{
			{"col1", "col2", "col3"},
			{"a", "b", "c"},
			{"x", "y", "z"},
		},
	})
	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx"}`))
	wantLines := []string{
		"col1 | col2 | col3",
		"a | b | c",
		"x | y | z",
	}
	for _, w := range wantLines {
		if !strings.Contains(res.Text, w) {
			t.Errorf("missing %q in output:\n%s", w, res.Text)
		}
	}
}

func TestReadDocx_SpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "a.docx", []string{
		`a < b & c > d`,
		`quotes " ' and ampersand &`,
	})
	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx"}`))
	if !strings.Contains(res.Text, "a < b & c > d") {
		t.Errorf("special chars not preserved; got %q", res.Text)
	}
}

func TestReadDocx_BrAndTab(t *testing.T) {
	dir := t.TempDir()
	// Build a docx with a <w:br/> and <w:tab/> in
	// a single paragraph. The renderer must
	// emit a newline for br and a tab for tab.
	path := filepath.Join(dir, "a.docx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p>
  <w:r><w:t>line1</w:t><w:br/><w:t>line2</w:t><w:tab/><w:t>col</w:t></w:r>
</w:p>
</w:body></w:document>`
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(doc))
	w.Close()
	f.Close()

	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.docx"}`))
	if !strings.Contains(res.Text, "line1\nline2\tcol") {
		t.Errorf("br/tab not handled; got %q", res.Text)
	}
}

func TestReadDocx_NotADocx(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("not a docx"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"plain.txt"}`))
	if res.Err == nil {
		t.Error("expected error for non-zip file")
	}
}

func TestReadDocx_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"nope.docx"}`))
	if res.Err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadDocx_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "path is required") {
		t.Errorf("expected 'path is required'; got %v", res.Err)
	}
}

func TestReadDocx_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	// 1 MB cap; write a 2 MB zip with a single
	// big document.xml (so we hit the cap).
	path := filepath.Join(dir, "big.docx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	big := strings.Repeat("X", 2*1024*1024)
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>` + big + `</w:t></w:r></w:p>
</w:body></w:document>`
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(doc))
	w.Close()
	f.Close()

	tool := NewReadDocx(dir, 1024*1024) // 1 MB cap
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"big.docx"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "too large") {
		t.Errorf("expected 'too large'; got %v", res.Err)
	}
}

func TestReadDocx_MaxParagraphsCap(t *testing.T) {
	dir := t.TempDir()
	paras := make([]string, 10)
	for i := range paras {
		paras[i] = "p"
	}
	writeTestDocx(t, dir, "a.docx", paras)
	tool := NewReadDocx(dir, 0)
	// max_paragraphs=3 → renderer should skip
	// the rest. Note: we count paragraphsSeen,
	// so 3 are rendered.
	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"a.docx","max_paragraphs":3}`))
	count := strings.Count(res.Text, "\n")
	if count != 3 {
		t.Errorf("expected 3 lines; got %d\n%s", count, res.Text)
	}
}

func TestReadDocx_RegisteredInRegistry(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadDocx(dir, 0)
	r := NewRegistry()
	if err := r.Register(tool.Spec()); err != nil {
		t.Fatal(err)
	}
	// Use a path that doesn't exist; the tool
	// returns a Go error.
	got, _ := r.Execute(context.Background(), "read_docx", json.RawMessage(`{"path":"a.docx"}`))
	if got.Err == nil {
		t.Error("expected missing-file error from registry Execute")
	}
}

func TestReadDocx_MalformedXML(t *testing.T) {
	dir := t.TempDir()
	// Real zip, valid [Content_Types].xml, broken
	// word/document.xml.
	path := filepath.Join(dir, "broken.docx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(`<w:document><w:body><w:p>UNCLOSED`))
	w.Close()
	f.Close()

	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"broken.docx"}`))
	if res.Err == nil {
		t.Error("expected parse error for malformed XML")
	}
}

func TestReadDocx_MissingDocumentEntry(t *testing.T) {
	dir := t.TempDir()
	// Zip without word/document.xml.
	path := filepath.Join(dir, "noentry.docx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Close()
	f.Close()

	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"noentry.docx"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not found") {
		t.Errorf("expected 'not found'; got %v", res.Err)
	}
}

func TestReadDocx_InterleavedParagraphsAndTables(t *testing.T) {
	dir := t.TempDir()
	// Two paragraphs, a table, then another
	// paragraph. The order must be preserved.
	writeTestDocxFull(t, dir, "a.docx",
		[]string{"before", "also-before"},
		&docxTableSpec{Rows: [][]string{{"1", "2"}, {"3", "4"}}},
	)
	// Append a third paragraph by re-opening
	// the zip — actually, the helper doesn't
	// support that. Let me build manually.
	path := filepath.Join(dir, "interleaved.docx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>first</w:t></w:r></w:p>
<w:tbl>
  <w:tr><w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc></w:tr>
</w:tbl>
<w:p><w:r><w:t>last</w:t></w:r></w:p>
</w:body></w:document>`
	fw, _ := w.Create("word/document.xml")
	fw.Write([]byte(doc))
	w.Close()
	f.Close()

	tool := NewReadDocx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"interleaved.docx"}`))
	// Order: "first\n", "a | b\n", "last\n".
	firstIdx := strings.Index(res.Text, "first")
	tabIdx := strings.Index(res.Text, "a | b")
	lastIdx := strings.Index(res.Text, "last")
	if firstIdx < 0 || tabIdx < 0 || lastIdx < 0 {
		t.Fatalf("expected all three parts; got:\n%s", res.Text)
	}
	if !(firstIdx < tabIdx && tabIdx < lastIdx) {
		t.Errorf("order wrong: first=%d table=%d last=%d", firstIdx, tabIdx, lastIdx)
	}
}

func TestReadDocx_Spec(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadDocx(dir, 0)
	spec := tool.Spec()
	if spec.Name != "read_docx" {
		t.Errorf("Name = %q, want read_docx", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("invalid read_docx spec: %v", err)
	}
}
