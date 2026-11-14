package catalog

import "testing"

func TestLoad(t *testing.T) {
	ps, err := Load([]Row{{SKU: "a", Price: "1.50"}, {SKU: "b", Price: "2", Sale: "1.75"}})
	if err != nil || len(ps) != 2 || ps[1].Sale != 175 {
		t.Fatalf("Load = %+v, %v", ps, err)
	}
}

func TestImporter(t *testing.T) {
	im := &Importer{Currency: "$"}
	if a, err := im.ParseAmount("$4.20"); err != nil || a != 420 {
		t.Fatalf("ParseAmount = %v, %v", a, err)
	}
	if _, err := im.ParseStrictFeed("$4.2"); err == nil {
		t.Fatal("strict feed accepted one decimal")
	}
}
