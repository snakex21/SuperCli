package tools

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// xlsxSpec describes an .xlsx we build in a
// test: a list of shared strings (referenced
// from cells by 0-based index) and a list of
// rows (each row a list of cells).
type xlsxSpec struct {
	Shared []string
	Rows   [][]xlsxCell
}

type xlsxCellKind int

const (
	cellShared xlsxCellKind = iota
	cellInline
	cellNumber
	cellBool
)

type xlsxCell struct {
	Kind  xlsxCellKind
	Value string // for cellShared: 0-based index; for cellInline: text; for cellNumber: number; for cellBool: "0" or "1"
}

// writeTestXlsx creates a real .xlsx zip at
// <dir>/<name> with the given spec.
func writeTestXlsx(t *testing.T, dir, name string, spec xlsxSpec) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()

	// [Content_Types].xml
	const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  <Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
  <Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>
</Types>`
	fw, _ := w.Create("[Content_Types].xml")
	fw.Write([]byte(contentTypes))

	// xl/workbook.xml — minimal workbook
	// manifest. IsXlsx looks for this entry
	// by name; the model doesn't read it.
	fw, _ = w.Create("xl/workbook.xml")
	fw.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`))

	// xl/sharedStrings.xml
	var sst strings.Builder
	sst.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range spec.Shared {
		sst.WriteString(`<si><t>`)
		xml.EscapeText(&sst, []byte(s))
		sst.WriteString(`</t></si>`)
	}
	sst.WriteString(`</sst>`)
	fw, _ = w.Create("xl/sharedStrings.xml")
	fw.Write([]byte(sst.String()))

	// xl/worksheets/sheet1.xml
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for i, row := range spec.Rows {
		sheet.WriteString(`<row r="`)
		sheet.WriteString(strconv.Itoa(i + 1))
		sheet.WriteString(`">`)
		for j, cell := range row {
			col := colLetter(j)
			sheet.WriteString(`<c r="`)
			sheet.WriteString(col)
			sheet.WriteString(strconv.Itoa(i + 1))
			sheet.WriteString(`"`)
			switch cell.Kind {
			case cellShared:
				sheet.WriteString(` t="s"><v>`)
				sheet.WriteString(xmlEscape(cell.Value))
				sheet.WriteString(`</v></c>`)
			case cellInline:
				sheet.WriteString(` t="inlineStr"><is><t>`)
				sheet.WriteString(xmlEscape(cell.Value))
				sheet.WriteString(`</t></is></c>`)
			case cellNumber:
				sheet.WriteString(`><v>`)
				sheet.WriteString(xmlEscape(cell.Value))
				sheet.WriteString(`</v></c>`)
			case cellBool:
				sheet.WriteString(` t="b"><v>`)
				sheet.WriteString(xmlEscape(cell.Value))
				sheet.WriteString(`</v></c>`)
			}
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	fw, _ = w.Create("xl/worksheets/sheet1.xml")
	fw.Write([]byte(sheet.String()))

	return path
}

func colLetter(n int) string {
	// 0 → A, 1 → B, ..., 25 → Z, 26 → AA
	var b strings.Builder
	for {
		b.WriteByte(byte('A' + n%26))
		n = n/26 - 1
		if n < 0 {
			break
		}
	}
	s := b.String()
	// reverse
	r := []byte(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func TestReadXlsx_BasicCells(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", xlsxSpec{
		Shared: []string{"alpha", "beta"},
		Rows: [][]xlsxCell{
			{{cellShared, "0"}, {cellShared, "1"}},
			{{cellNumber, "42"}, {cellNumber, "3.14"}},
		},
	})
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx"}`))
	want := "alpha | beta\n42 | 3.14"
	if res.Text != want {
		t.Errorf("got %q\nwant %q", res.Text, want)
	}
}

func TestReadXlsx_InlineStrings(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", xlsxSpec{
		Rows: [][]xlsxCell{
			{{cellInline, "hello"}, {cellInline, "world"}},
		},
	})
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx"}`))
	if res.Text != "hello | world" {
		t.Errorf("got %q", res.Text)
	}
}

func TestReadXlsx_Booleans(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", xlsxSpec{
		Rows: [][]xlsxCell{
			{{cellBool, "1"}, {cellBool, "0"}, {cellBool, "1"}},
		},
	})
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx"}`))
	if res.Text != "TRUE | FALSE | TRUE" {
		t.Errorf("got %q", res.Text)
	}
}

func TestReadXlsx_NoSharedStrings(t *testing.T) {
	dir := t.TempDir()
	// Hand-build a sheet without a sharedStrings.xml entry.
	path := filepath.Join(dir, "a.xlsx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Create("xl/workbook.xml")
	var sheet strings.Builder
	sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><v>99</v></c></row></sheetData></worksheet>`)
	fw, _ := w.Create("xl/worksheets/sheet1.xml")
	fw.Write([]byte(sheet.String()))
	w.Close()
	f.Close()

	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx"}`))
	if res.Text != "99" {
		t.Errorf("got %q", res.Text)
	}
}

func TestReadXlsx_SheetByNumber(t *testing.T) {
	dir := t.TempDir()
	// Build a workbook with two sheets, and
	// read the second one.
	path := filepath.Join(dir, "a.xlsx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Create("xl/workbook.xml")
	w.Create("xl/sharedStrings.xml")
	sheet1 := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><v>1</v></c></row></sheetData></worksheet>`
	sheet2 := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><v>2</v></c></row></sheetData></worksheet>`
	fw, _ := w.Create("xl/worksheets/sheet1.xml")
	fw.Write([]byte(sheet1))
	fw, _ = w.Create("xl/worksheets/sheet2.xml")
	fw.Write([]byte(sheet2))
	w.Close()
	f.Close()

	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx","sheet":"2"}`))
	if res.Text != "2" {
		t.Errorf("got %q", res.Text)
	}
}

func TestReadXlsx_SheetByName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.xlsx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Create("xl/workbook.xml")
	w.Create("xl/sharedStrings.xml")
	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><v>7</v></c></row></sheetData></worksheet>`
	fw, _ := w.Create("xl/worksheets/Sheet1.xml")
	fw.Write([]byte(sheet))
	w.Close()
	f.Close()

	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx","sheet":"Sheet1"}`))
	if res.Text != "7" {
		t.Errorf("got %q", res.Text)
	}
}

func TestReadXlsx_InvalidSheetName(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", xlsxSpec{
		Rows: [][]xlsxCell{{{cellNumber, "1"}}},
	})
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx","sheet":"../evil"}`))
	if res.Err == nil {
		t.Error("expected error for path-traversal sheet name")
	}
}

func TestReadXlsx_MaxCellsCap(t *testing.T) {
	dir := t.TempDir()
	rows := [][]xlsxCell{
		{{cellNumber, "1"}, {cellNumber, "2"}, {cellNumber, "3"}},
		{{cellNumber, "4"}, {cellNumber, "5"}, {cellNumber, "6"}},
	}
	writeTestXlsx(t, dir, "a.xlsx", xlsxSpec{Rows: rows})
	tool := NewReadXlsx(dir, 0)
	// max_cells=3 → 3 cells rendered, 3 dropped.
	res, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"path":"a.xlsx","max_cells":3}`))
	if strings.Count(res.Text, "|") != 2 {
		// 2 pipes for 3 cells in row 1, no row 2
		t.Errorf("expected 2 pipes (row 1 only); got %q", res.Text)
	}
}

func TestReadXlsx_NotAXlsx(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("not an xlsx"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"plain.txt"}`))
	if res.Err == nil {
		t.Error("expected error for non-zip file")
	}
}

func TestReadXlsx_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"nope.xlsx"}`))
	if res.Err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadXlsx_EmptyPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "path is required") {
		t.Errorf("expected 'path is required'; got %v", res.Err)
	}
}

func TestReadXlsx_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	// Build an xlsx with a sheet1.xml bigger
	// than the cap.
	path := filepath.Join(dir, "big.xlsx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Create("xl/workbook.xml")
	big := strings.Repeat("X", 2*1024*1024)
	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><v>` + big + `</v></c></row></sheetData></worksheet>`
	fw, _ := w.Create("xl/worksheets/sheet1.xml")
	fw.Write([]byte(sheet))
	w.Close()
	f.Close()

	tool := NewReadXlsx(dir, 1024*1024) // 1 MB cap
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"big.xlsx"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "too large") {
		t.Errorf("expected 'too large'; got %v", res.Err)
	}
}

func TestReadXlsx_RegisteredInRegistry(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadXlsx(dir, 0)
	r := NewRegistry()
	if err := r.Register(tool.Spec()); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Execute(context.Background(), "read_xlsx", json.RawMessage(`{"path":"a.xlsx"}`))
	if got.Err == nil {
		t.Error("expected missing-file error from registry Execute")
	}
}

func TestReadXlsx_SpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", xlsxSpec{
		Shared: []string{`a < b & c > "d"`, "ampersand &"},
		Rows: [][]xlsxCell{
			{{cellShared, "0"}, {cellShared, "1"}},
		},
	})
	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx"}`))
	if !strings.Contains(res.Text, `a < b & c > "d"`) {
		t.Errorf("special chars not preserved; got %q", res.Text)
	}
}

func TestReadXlsx_OutOfRangeSharedIndex(t *testing.T) {
	dir := t.TempDir()
	// Build a sheet that references shared
	// index 5, but the shared table has only
	// 2 entries → must yield empty cell, not
	// crash.
	path := filepath.Join(dir, "a.xlsx")
	f, _ := os.Create(path)
	w := zip.NewWriter(f)
	w.Create("[Content_Types].xml")
	w.Create("xl/workbook.xml")
	sst := `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>only</t></si></sst>`
	fw, _ := w.Create("xl/sharedStrings.xml")
	fw.Write([]byte(sst))
	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>5</v></c></row></sheetData></worksheet>`
	fw, _ = w.Create("xl/worksheets/sheet1.xml")
	fw.Write([]byte(sheet))
	w.Close()
	f.Close()

	tool := NewReadXlsx(dir, 0)
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx"}`))
	if res.Err != nil {
		t.Errorf("expected no error; got %v", res.Err)
	}
}

func TestReadXlsx_Spec(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadXlsx(dir, 0)
	spec := tool.Spec()
	if spec.Name != "read_xlsx" {
		t.Errorf("Name = %q, want read_xlsx", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

func TestReadXlsx_IsXlsx(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "real.xlsx", xlsxSpec{
		Rows: [][]xlsxCell{{{cellNumber, "1"}}},
	})
	if !IsXlsx(filepath.Join(dir, "real.xlsx")) {
		t.Error("IsXlsx should return true for a real .xlsx")
	}
	// docx is not xlsx
	writeTestDocx(t, dir, "fake.xlsx", []string{"hi"})
	if IsXlsx(filepath.Join(dir, "fake.xlsx")) {
		t.Error("IsXlsx should return false for a docx")
	}
	// missing file
	if IsXlsx(filepath.Join(dir, "missing.xlsx")) {
		t.Error("IsXlsx should return false for missing file")
	}
}
