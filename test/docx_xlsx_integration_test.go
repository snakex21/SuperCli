//go:build integration

package test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools"
)

// --- helpers ---

func writeTestDocx(t *testing.T, dir, name string, paragraphs []string) string {
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

type xlsxSpec struct {
	Rows [][]string
}

func writeTestXlsx(t *testing.T, dir, name string, spec xlsxSpec) string {
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
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
</Types>`
	if fw, err := w.Create("[Content_Types].xml"); err != nil {
		t.Fatal(err)
	} else if _, err := fw.Write([]byte(contentTypes)); err != nil {
		t.Fatal(err)
	}

	// xl/worksheets/sheet1.xml
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range spec.Rows {
		sheet.WriteString(`<row r="`)
		sheet.WriteString(string(rune('1' + r)))
		sheet.WriteString(`">`)
		for c, cell := range row {
			col := string(rune('A' + c))
			sheet.WriteString(`<c r="`)
			sheet.WriteString(col)
			sheet.WriteString(string(rune('1' + r)))
			sheet.WriteString(`" t="inlineStr"><is><t>`)
			xml.EscapeText(&sheet, []byte(cell))
			sheet.WriteString(`</t></is></c>`)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	if fw, err := w.Create("xl/worksheets/sheet1.xml"); err != nil {
		t.Fatal(err)
	} else if _, err := fw.Write([]byte(sheet.String())); err != nil {
		t.Fatal(err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- docx integration test ---

func TestIntegration_ReadDocx_FindsText(t *testing.T) {
	dir := t.TempDir()
	writeTestDocx(t, dir, "test.docx", []string{"lubię placki", "i inne rzeczy"})

	tool := tools.NewReadDocx(dir, 0)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "read_docx", json.RawMessage(`{"path":"test.docx"}`))
	if err != nil {
		t.Fatalf("execute read_docx: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("read_docx result error: %v", res.Err)
	}
	if !strings.Contains(res.Text, "lubię placki") {
		t.Errorf("expected 'lubię placki' in output, got: %s", res.Text)
	}
}

func TestIntegration_DocxPipeline_ReadEditVerify(t *testing.T) {
	dir := t.TempDir()
	path := writeTestDocx(t, dir, "test.docx", []string{"lubię placki", "drugi akapit"})

	reg := tools.NewRegistry()
	docxTool := tools.NewReadDocx(dir, 0)
	reg.MustRegister(docxTool.Spec())

	// Step 1: read docx — verify original text.
	res, err := reg.Execute(context.Background(), "read_docx", json.RawMessage(`{"path":"test.docx"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Text, "lubię placki") {
		t.Fatalf("step 1: 'lubię placki' not found: %s", res.Text)
	}
	t.Logf("step 1 ok: %q", res.Text)

	// Step 2: edit — open the zip, replace text in word/document.xml,
	// then write back. This simulates what a write_docx tool would do.
	err = replaceInZipXML(path, "word/document.xml", "lubię placki", "nie lubię placków")
	if err != nil {
		t.Fatalf("step 2 edit: %v", err)
	}
	t.Logf("step 2: replaced 'lubię placki' → 'nie lubię placków'")

	// Step 3: verify the change via read_docx.
	res2, err := reg.Execute(context.Background(), "read_docx", json.RawMessage(`{"path":"test.docx"}`))
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if !strings.Contains(res2.Text, "nie lubię placków") {
		t.Errorf("step 3: 'nie lubię placków' not found: %s", res2.Text)
	}
	if strings.Contains(res2.Text, "lubię placki") {
		t.Errorf("step 3: old text 'lubię placki' still present")
	}
	t.Logf("step 3 ok: %q", res2.Text)
}

func TestIntegration_XlsxPipeline_ReadEditVerify(t *testing.T) {
	dir := t.TempDir()
	path := writeTestXlsx(t, dir, "test.xlsx", xlsxSpec{
		Rows: [][]string{
			{"Produkt", "Cena"},
			{"Chleb", "5.50"},
			{"Mleko", "3.20"},
		},
	})

	reg := tools.NewRegistry()
	xlsxTool := tools.NewReadXlsx(dir, 0)
	reg.MustRegister(xlsxTool.Spec())

	// Step 1: read original.
	res, err := reg.Execute(context.Background(), "read_xlsx", json.RawMessage(`{"path":"test.xlsx"}`))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(res.Text, "Chleb") || !strings.Contains(res.Text, "5.50") {
		t.Fatalf("step 1: original data not found: %s", res.Text)
	}
	t.Logf("step 1 ok: %q", res.Text)

	// Step 2: edit — replace "Chleb" with "Bagietka" in xl/worksheets/sheet1.xml.
	err = replaceInZipXML(path, "xl/worksheets/sheet1.xml", "Chleb", "Bagietka")
	if err != nil {
		t.Fatalf("step 2 edit: %v", err)
	}
	t.Logf("step 2: replaced 'Chleb' → 'Bagietka'")

	// Step 3: verify change.
	res2, err := reg.Execute(context.Background(), "read_xlsx", json.RawMessage(`{"path":"test.xlsx"}`))
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if !strings.Contains(res2.Text, "Bagietka") {
		t.Errorf("step 3: 'Bagietka' not found: %s", res2.Text)
	}
	if strings.Contains(res2.Text, "Chleb") {
		t.Errorf("step 3: old text 'Chleb' still present")
	}
	t.Logf("step 3 ok: %q", res2.Text)
}

// replaceInZipXML opens a zip file, reads entry name, replaces
// oldText with newText, and writes the modified entry back.
func replaceInZipXML(zipPath, entryName, oldText, newText string) error {
	// Read original zip.
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}

	// Find entry and read its content.
	var entryData []byte
	for _, f := range reader.File {
		if f.Name == entryName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			entryData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
			break
		}
	}
	if entryData == nil {
		return os.ErrNotExist
	}

	// Replace text.
	modified := strings.Replace(string(entryData), oldText, newText, 1)
	if modified == string(entryData) {
		return os.ErrInvalid // oldText not found
	}

	// Write new zip with modified entry.
	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()
	writer := zip.NewWriter(out)
	for _, f := range reader.File {
		var content []byte
		if f.Name == entryName {
			content = []byte(modified)
		} else {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			content, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
		}
		fw, err := writer.Create(f.Name)
		if err != nil {
			return err
		}
		if _, err := fw.Write(content); err != nil {
			return err
		}
	}
	return writer.Close()
}

// --- xlsx integration test ---

func TestIntegration_ReadXlsx_FindsData(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "test.xlsx", xlsxSpec{
		Rows: [][]string{
			{"Imię", "Wiek", "Miasto"},
			{"Anna", "28", "Kraków"},
			{"Jan", "35", "Warszawa"},
		},
	})

	tool := tools.NewReadXlsx(dir, 0)
	reg := tools.NewRegistry()
	reg.MustRegister(tool.Spec())

	res, err := reg.Execute(context.Background(), "read_xlsx", json.RawMessage(`{"path":"test.xlsx"}`))
	if err != nil {
		t.Fatalf("execute read_xlsx: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("read_xlsx result error: %v", res.Err)
	}
	for _, want := range []string{"Anna", "Kraków", "Warszawa", "Imię"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("expected %q in output, got: %s", want, res.Text)
		}
	}
}

// --- helpers ---

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var s []byte
	for n > 0 {
		s = append([]byte{byte('0' + n%10)}, s...)
		n /= 10
	}
	return string(s)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
