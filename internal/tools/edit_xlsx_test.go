package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runEditXlsx(t *testing.T, tool *EditXlsxTool, args string) Result {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("edit_xlsx error: %v", err)
	}
	return res
}

func readXlsxText(t *testing.T, dir, name string) string {
	t.Helper()
	rd := NewReadXlsx(dir, 0)
	res, err := rd.Execute(context.Background(), json.RawMessage(fmt.Sprintf(`{"path":%q}`, name)))
	if err != nil {
		t.Fatalf("read_xlsx error: %v", err)
	}
	return res.Text
}

func twoByTwo() xlsxSpec {
	return xlsxSpec{
		Shared: []string{"Name", "Total"},
		Rows: [][]xlsxCell{
			{{Kind: cellShared, Value: "0"}, {Kind: cellShared, Value: "1"}},
			{{Kind: cellInline, Value: "Alice"}, {Kind: cellNumber, Value: "10"}},
		},
	}
}

func TestEditXlsx_SetCellOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)

	runEditXlsx(t, tool, `{"path":"a.xlsx","action":"set_cell","cell":"B2","value":42.5}`)
	got := readXlsxText(t, dir, "a.xlsx")
	if !strings.Contains(got, "Alice | 42.5") {
		t.Fatalf("set_cell numeric failed: %q", got)
	}
	// Backup with the old value.
	if _, err := os.Stat(filepath.Join(dir, "a.xlsx.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if bak := readXlsxText(t, dir, "a.xlsx.bak"); !strings.Contains(bak, "Alice | 10") {
		t.Fatalf("backup content wrong: %q", bak)
	}
}

func TestEditXlsx_SetCellStringInline(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)
	runEditXlsx(t, tool, `{"path":"a.xlsx","action":"set_cell","cell":"A2","value":"Bob & Co <x>"}`)
	got := readXlsxText(t, dir, "a.xlsx")
	if !strings.Contains(got, "Bob & Co <x> | 10") {
		t.Fatalf("string cell failed: %q", got)
	}
}

func TestEditXlsx_SetCellNewCellInRow(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)
	runEditXlsx(t, tool, `{"path":"a.xlsx","action":"set_cell","cell":"C1","value":"Notes"}`)
	got := readXlsxText(t, dir, "a.xlsx")
	if !strings.Contains(got, "Name | Total | Notes") {
		t.Fatalf("new cell in existing row failed: %q", got)
	}
}

func TestEditXlsx_SetCellNewRow(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)
	runEditXlsx(t, tool, `{"path":"a.xlsx","action":"set_cell","cell":"A5","value":"Footer"}`)
	got := readXlsxText(t, dir, "a.xlsx")
	lines := strings.Split(got, "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "Footer") {
		t.Fatalf("new row failed: %q", got)
	}
}

func TestEditXlsx_AppendRow(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)
	res := runEditXlsx(t, tool, `{"path":"a.xlsx","action":"append_row","values":["Carol", 99, true]}`)
	if !strings.Contains(res.Text, "Appended row 3") {
		t.Fatalf("unexpected: %q", res.Text)
	}
	got := readXlsxText(t, dir, "a.xlsx")
	if !strings.Contains(got, "Carol | 99 | TRUE") {
		t.Fatalf("append_row failed: %q", got)
	}
}

func TestEditXlsx_RoundTripMultipleEdits(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)
	runEditXlsx(t, tool, `{"path":"a.xlsx","action":"append_row","values":["Dave", 7]}`)
	runEditXlsx(t, tool, `{"path":"a.xlsx","action":"set_cell","cell":"B3","value":8}`)
	got := readXlsxText(t, dir, "a.xlsx")
	if !strings.Contains(got, "Dave | 8") {
		t.Fatalf("multi-edit round trip failed: %q", got)
	}
	// Shared strings from the original file must survive
	// (we never rewrite sharedStrings.xml).
	if !strings.Contains(got, "Name | Total") {
		t.Fatalf("original shared strings lost: %q", got)
	}
}

func TestEditXlsx_BadCellRef(t *testing.T) {
	dir := t.TempDir()
	writeTestXlsx(t, dir, "a.xlsx", twoByTwo())
	tool := NewEditXlsx(dir)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"a.xlsx","action":"set_cell","cell":"nope","value":1}`))
	if err == nil || !strings.Contains(err.Error(), "bad cell reference") {
		t.Fatalf("want bad-ref error, got %v", err)
	}
}

func TestEditXlsx_MissingFile(t *testing.T) {
	tool := NewEditXlsx(t.TempDir())
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"none.xlsx","action":"set_cell","cell":"A1","value":1}`))
	if err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestEditXlsx_Spec(t *testing.T) {
	spec := NewEditXlsx(".").Spec()
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if spec.Name != "edit_xlsx" {
		t.Fatalf("name: %q", spec.Name)
	}
	if !strings.Contains(spec.Description, "LIMITATION") {
		t.Error("description must document the no-formula/no-style limitation")
	}
}

func TestColHelpers(t *testing.T) {
	cases := map[string]int{"A": 1, "B": 2, "Z": 26, "AA": 27, "AB": 28}
	for col, idx := range cases {
		if got := colToIndex(col); got != idx {
			t.Errorf("colToIndex(%s)=%d want %d", col, got, idx)
		}
		if got := indexToCol(idx); got != col {
			t.Errorf("indexToCol(%d)=%s want %s", idx, got, col)
		}
	}
}
