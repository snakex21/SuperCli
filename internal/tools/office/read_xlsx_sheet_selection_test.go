package office

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const namedWorkbookXML = `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sales 2026" sheetId="7" r:id="rId7"/><sheet name="Січень 東京" sheetId="9" r:id="rId9"/><sheet name="Empty" sheetId="11" r:id="rId11"/><sheet name="sheet1" sheetId="12" r:id="rId12"/></sheets></workbook>`
const namedWorkbookRels = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet7.xml"/><Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="/xl/worksheets/sheet9.xml"/><Relationship Id="rId11" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet11.xml"/><Relationship Id="rId12" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet12.xml"/></Relationships>`

func xlsxNumberSheet(value string) string {
	return `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><v>` + value + `</v></c></row></sheetData></worksheet>`
}

func namedWorkbookEntries() map[string]string {
	return map[string]string{
		"xl/workbook.xml":            namedWorkbookXML,
		"xl/_rels/workbook.xml.rels": namedWorkbookRels,
		"xl/worksheets/sheet1.xml":   xlsxNumberSheet("1"),
		"xl/worksheets/sheet7.xml":   xlsxNumberSheet("7026"),
		"xl/worksheets/sheet9.xml":   xlsxNumberSheet("9000"),
		"xl/worksheets/sheet11.xml":  `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`,
		"xl/worksheets/sheet12.xml":  xlsxNumberSheet("12"),
	}
}

func writeWorkbookEntries(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "book.xlsx")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func readWorkbookSheet(t *testing.T, p, sheet string, tool *ReadXlsxTool) (Result, error) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"path": filepath.Base(p), "sheet": sheet})
	if err != nil {
		t.Fatal(err)
	}
	if tool == nil {
		tool = NewReadXlsx(filepath.Dir(p), 0)
	}
	return tool.Execute(context.Background(), args)
}

func TestReadXlsx_LogicalSheetNames(t *testing.T) {
	p := writeWorkbookEntries(t, namedWorkbookEntries())
	for _, tc := range []struct{ sheet, want string }{
		{"Sales 2026", "7026"}, {"Січень 東京", "9000"}, {"sheet1", "12"}, {"Empty", ""},
	} {
		t.Run(tc.sheet, func(t *testing.T) {
			res, err := readWorkbookSheet(t, p, tc.sheet, nil)
			if err != nil || res.Err != nil || res.Text != tc.want {
				t.Fatalf("sheet %q: text=%q Err=%v err=%v; want %q", tc.sheet, res.Text, res.Err, err, tc.want)
			}
		})
	}
}

func TestReadXlsx_PreservesPhysicalNumericSelection(t *testing.T) {
	entries := namedWorkbookEntries()
	// Numeric/default selection still targets sheet1, independently of logical ordering.
	entries["xl/workbook.xml"] = "<broken"
	p := writeWorkbookEntries(t, entries)
	for _, sheet := range []string{"", "1", "01"} {
		res, err := readWorkbookSheet(t, p, sheet, nil)
		if err != nil || res.Err != nil || res.Text != "1" {
			t.Fatalf("sheet %q: %+v err=%v", sheet, res, err)
		}
	}
}

func TestReadXlsx_UnknownLogicalSheetReportsAvailableNames(t *testing.T) {
	p := writeWorkbookEntries(t, namedWorkbookEntries())
	res, err := readWorkbookSheet(t, p, "Missing", nil)
	if err == nil || res.Err == nil {
		t.Fatalf("unknown sheet became successful empty output: %+v err=%v", res, err)
	}
	for _, name := range []string{"Missing", "Sales 2026", "Січень 東京", "Empty"} {
		if !strings.Contains(res.Err.Error(), name) {
			t.Errorf("error %q omitted %q", res.Err, name)
		}
	}
}

func TestReadXlsx_MissingWorksheetIsError(t *testing.T) {
	entries := namedWorkbookEntries()
	delete(entries, "xl/worksheets/sheet7.xml")
	p := writeWorkbookEntries(t, entries)
	for _, sheet := range []string{"Sales 2026", "7"} {
		res, err := readWorkbookSheet(t, p, sheet, nil)
		if err == nil || res.Err == nil {
			t.Fatalf("missing entry for %q became empty success: %+v err=%v", sheet, res, err)
		}
	}
}

func TestReadXlsx_RejectsInvalidWorkbookRelationships(t *testing.T) {
	for _, tc := range []struct{ name, workbook, rels, want string }{
		{"malformed workbook", "<workbook>", namedWorkbookRels, "parse workbook.xml"},
		{"malformed relationships", namedWorkbookXML, "<Relationships>", "parse workbook.xml.rels"},
		{"missing relationship", namedWorkbookXML, `<Relationships/>`, "relationship"},
		{"external target", namedWorkbookXML, `<Relationships><Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" TargetMode="External" Target="https://example.org/sheet.xml"/></Relationships>`, "external"},
		{"outside worksheet directory", namedWorkbookXML, `<Relationships><Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="../../outside.xml"/></Relationships>`, "invalid"},
		{"wrong part type", namedWorkbookXML, `<Relationships><Relationship Id="rId7" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/chartsheet" Target="worksheets/sheet7.xml"/></Relationships>`, "worksheet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := namedWorkbookEntries()
			entries["xl/workbook.xml"], entries["xl/_rels/workbook.xml.rels"] = tc.workbook, tc.rels
			p := writeWorkbookEntries(t, entries)
			res, err := readWorkbookSheet(t, p, "Sales 2026", nil)
			if err == nil || res.Err == nil || !strings.Contains(res.Err.Error(), tc.want) {
				t.Fatalf("got %+v err=%v; want error containing %q", res, err, tc.want)
			}
		})
	}
}

func TestReadXlsx_SheetMetadataByteCap(t *testing.T) {
	entries := namedWorkbookEntries()
	entries["xl/workbook.xml"] = strings.Replace(namedWorkbookXML, "</workbook>", "<padding>"+strings.Repeat("x", 8192)+"</padding></workbook>", 1)
	p := writeWorkbookEntries(t, entries)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewReadXlsx(filepath.Dir(p), info.Size()+1)
	res, err := readWorkbookSheet(t, p, "Sales 2026", tool)
	if err == nil || res.Err == nil || !strings.Contains(res.Err.Error(), "too large") {
		t.Fatalf("metadata cap was bypassed: %+v err=%v", res, err)
	}
}

func TestReadXlsx_NamedSheetKeepsCellAndOutputLimits(t *testing.T) {
	entries := namedWorkbookEntries()
	entries["xl/worksheets/sheet7.xml"] = `<worksheet><sheetData><row><c><v>10</v></c><c><v>20</v></c></row></sheetData></worksheet>`
	p := writeWorkbookEntries(t, entries)
	tool := NewReadXlsx(filepath.Dir(p), 0)
	tool.MaxCells = 1
	res, err := readWorkbookSheet(t, p, "Sales 2026", tool)
	if err != nil || res.Err != nil || res.Text != "10" {
		t.Fatalf("cell cap changed: %+v err=%v", res, err)
	}
	tool.MaxOutputBytes = 1
	res, err = readWorkbookSheet(t, p, "Sales 2026", tool)
	if err == nil || res.Err == nil || !strings.Contains(res.Err.Error(), "rendered text") {
		t.Fatalf("output cap changed: %+v err=%v", res, err)
	}
}

func TestReadXlsx_WorksheetTargetURIResolution(t *testing.T) {
	for _, tc := range []struct{ target, entry string }{
		{"worksheets/Sales%202026.xml", "xl/worksheets/Sales 2026.xml"},
		{"/xl/worksheets/sheet7.xml", "xl/worksheets/sheet7.xml"},
		{"../tables/sheet7.xml", "tables/sheet7.xml"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			entries := namedWorkbookEntries()
			entries["xl/_rels/workbook.xml.rels"] = strings.Replace(namedWorkbookRels, "worksheets/sheet7.xml", tc.target, 1)
			entries[tc.entry] = xlsxNumberSheet("123")
			p := writeWorkbookEntries(t, entries)
			before, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			res, err := readWorkbookSheet(t, p, "Sales 2026", nil)
			if err != nil || res.Err != nil || res.Text != "123" {
				t.Fatalf("URI target %q: %+v err=%v", tc.target, res, err)
			}
			after, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("read_xlsx changed workbook bytes")
			}
		})
	}
}

func TestReadXlsx_RejectsNonLocalWorksheetURI(t *testing.T) {
	for _, target := range []string{"https://example.org/sheet.xml", "//server/sheet.xml", "../../outside.xml", "/../outside.xml", "worksheets/sheet7.xml?x=1", "worksheets/sheet7.xml#part", "worksheets/%5Csheet7.xml", "worksheets/"} {
		if _, err := xlsxWorksheetTarget(target); err == nil {
			t.Errorf("accepted nonlocal/invalid worksheet target %q", target)
		}
	}
}

func TestReadXlsx_MissingRelationshipsAndCanceledContext(t *testing.T) {
	entries := namedWorkbookEntries()
	delete(entries, "xl/_rels/workbook.xml.rels")
	p := writeWorkbookEntries(t, entries)
	res, err := readWorkbookSheet(t, p, "Sales 2026", nil)
	if err == nil || res.Err == nil || !strings.Contains(res.Err.Error(), "relationship") {
		t.Fatalf("missing relationships became success: %+v err=%v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewReadXlsx(filepath.Dir(p), 0)
	res, err = tool.Execute(ctx, json.RawMessage(`{"path":"book.xlsx","sheet":"Sales 2026"}`))
	if err != context.Canceled || res.Err != context.Canceled {
		t.Fatalf("canceled call: %+v err=%v", res, err)
	}
}

func TestReadXlsx_AvailableNamesAreBounded(t *testing.T) {
	sheets := make([]xlsxWorkbookSheet, 100)
	for i := range sheets {
		sheets[i].Name = strings.Repeat("東京", 1000)
	}
	names := xlsxAvailableSheetNames(sheets)
	if len(names) > 4096 || !strings.Contains(names, "and 92 more") || !strings.Contains(names, "…") {
		t.Fatalf("unbounded or silently truncated names: %d bytes", len(names))
	}
}
