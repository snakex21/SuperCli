package invoicing

import (
	"fmt"
	"testing"
)

var sample = []Line{
	{Name: "Kawa", Qty: 2, UnitCents: 1250},
	{Name: "Laptop", Qty: 1, UnitCents: 512399},
}

func TestRenderInvoice(t *testing.T) {
	want := "Invoice FV/1\n" +
		fmt.Sprintf("%-20s %4d x %12s = %14s\n", "Kawa", 2, "12.50", "25.00") +
		fmt.Sprintf("%-20s %4d x %12s = %14s\n", "Laptop", 1, "5,123.99", "5,123.99") +
		fmt.Sprintf("%-20s %35s\n", "TOTAL", "5,148.99")
	if got := RenderInvoice("FV/1", sample); got != want {
		t.Errorf("RenderInvoice =\n%s\nwant\n%s", got, want)
	}
}

func TestSummary(t *testing.T) {
	if got, want := Summary(sample), "2 positions, total 5,148.99 PLN"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestExportCSV(t *testing.T) {
	want := "name;qty;unit;total\nKawa;2;12.50;25.00\nLaptop;1;5,123.99;5,123.99\nTOTAL;;;5,148.99\n"
	if got := ExportCSV(sample); got != want {
		t.Errorf("ExportCSV = %q, want %q", got, want)
	}
}
