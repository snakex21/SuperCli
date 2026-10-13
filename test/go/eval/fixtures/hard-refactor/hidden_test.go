//go:build evalhidden

package invoicing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHiddenFormatAmount(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{-5, "-0.05"},
		{99, "0.99"},
		{100, "1.00"},
		{-100, "-1.00"},
		{105, "1.05"},
		{99999, "999.99"},
		{100000, "1,000.00"},
		{-100000, "-1,000.00"},
		{12345678, "123,456.78"},
		{-12345678, "-123,456.78"},
		{123456789, "1,234,567.89"},
		{math.MaxInt64, "92,233,720,368,547,758.07"},
		{math.MinInt64, "-92,233,720,368,547,758.08"},
	}
	for _, c := range cases {
		if got := FormatAmount(c.in); got != c.want {
			t.Errorf("FormatAmount(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHiddenOutputsUseSharedFormatting(t *testing.T) {
	lines := []Line{
		{Name: "Rabat", Qty: 1, UnitCents: -12345},
		{Name: "Drobne", Qty: 3, UnitCents: 5},
		{Name: "Serwer", Qty: 2, UnitCents: 1234567890},
	}
	tot := total(lines)
	wantInvoice := "Invoice X\n" +
		fmt.Sprintf("%-20s %4d x %12s = %14s\n", "Rabat", 1, "-123.45", "-123.45") +
		fmt.Sprintf("%-20s %4d x %12s = %14s\n", "Drobne", 3, "0.05", "0.15") +
		fmt.Sprintf("%-20s %4d x %12s = %14s\n", "Serwer", 2, "12,345,678.90", "24,691,357.80") +
		fmt.Sprintf("%-20s %35s\n", "TOTAL", FormatAmount(tot))
	if got := RenderInvoice("X", lines); got != wantInvoice {
		t.Errorf("RenderInvoice =\n%s\nwant\n%s", got, wantInvoice)
	}
	if FormatAmount(tot) != "24,691,234.50" {
		t.Errorf("FormatAmount(total) = %q, want 24,691,234.50", FormatAmount(tot))
	}
	if got, want := Summary(lines), "3 positions, total 24,691,234.50 PLN"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	wantCSV := "name;qty;unit;total\nRabat;1;-123.45;-123.45\nDrobne;3;0.05;0.15\nSerwer;2;12,345,678.90;24,691,357.80\nTOTAL;;;24,691,234.50\n"
	if got := ExportCSV(lines); got != wantCSV {
		t.Errorf("ExportCSV = %q, want %q", got, wantCSV)
	}
	huge := []Line{{Name: "Max", Qty: 1, UnitCents: math.MaxInt64 - 1}}
	if got, want := Summary(huge), "1 positions, total 92,233,720,368,547,758.06 PLN"; got != want {
		t.Errorf("Summary(huge) = %q, want %q", got, want)
	}
	if got, want := Summary(nil), "0 positions, total 0.00 PLN"; got != want {
		t.Errorf("Summary(nil) = %q, want %q", got, want)
	}
}

func TestHiddenStructure(t *testing.T) {
	if _, err := os.Stat("money.go"); err != nil {
		t.Fatalf("money.go must exist: %v", err)
	}
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	declaredIn := map[string]string{}
	callsFormatAmount := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				declaredIn[fn.Name.Name] = f
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "FormatAmount" {
					callsFormatAmount[f] = true
				}
			}
			return true
		})
	}
	if declaredIn["FormatAmount"] != "money.go" {
		t.Errorf("FormatAmount must be declared in money.go, found in %q", declaredIn["FormatAmount"])
	}
	for _, old := range []string{"formatCents", "moneyStr", "amountText"} {
		if f, ok := declaredIn[old]; ok {
			t.Errorf("old helper %s is still declared in %s", old, f)
		}
	}
	for _, f := range []string{"invoice.go", "summary.go", "csvexport.go"} {
		if !callsFormatAmount[f] {
			t.Errorf("%s does not call FormatAmount", f)
		}
	}
}
