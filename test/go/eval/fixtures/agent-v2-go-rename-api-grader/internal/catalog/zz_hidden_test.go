package catalog

import "testing"

func TestHiddenImporterKeepsMethodName(t *testing.T) {
	im := &Importer{Currency: "EUR"}
	if a, err := im.ParseAmount("EUR 3.10"); err != nil || a != 310 {
		t.Fatalf("Importer.ParseAmount = %v, %v", a, err)
	}
}
