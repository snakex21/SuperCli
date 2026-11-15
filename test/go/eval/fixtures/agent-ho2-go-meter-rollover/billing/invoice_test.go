package billing

import (
	"os"
	"path/filepath"
	"testing"

	"meterd/tariff"
)

func open(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "testdata", "march", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestMarchInvoices(t *testing.T) {
	invs, err := Build(open(t, "meters.csv"), open(t, "readings.csv"), tariff.Household)
	if err != nil {
		t.Fatal(err)
	}
	if len(invs) != 30 {
		t.Fatalf("got %d invoices, want 30", len(invs))
	}
	want := map[string][2]int64{
		"C-0043": {422, 29112},
		"C-0016": {502, 34792},
		"C-0025": {1406, 104660},
	}
	for _, inv := range invs {
		if w, ok := want[inv.Customer]; ok && (inv.KWh != w[0] || inv.Amount != w[1]) {
			t.Errorf("%s: got %d kWh / %s, want %d kWh / %s", inv.Customer, inv.KWh, FormatPLN(inv.Amount), w[0], FormatPLN(w[1]))
		}
	}
	kwh, amount := Total(invs)
	if kwh != 14875 || amount != 1036309 {
		t.Fatalf("total: got %d kWh / %s, want 14875 kWh / %s", kwh, FormatPLN(amount), FormatPLN(1036309))
	}
}

func TestFormatPLN(t *testing.T) {
	for in, want := range map[int64]string{0: "0,00 zł", 123456: "1 234,56 zł", -5: "-0,05 zł"} {
		if got := FormatPLN(in); got != want {
			t.Errorf("FormatPLN(%d) = %q, want %q", in, got, want)
		}
	}
}
