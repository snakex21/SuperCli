package users

import "testing"

func TestStoreExport(t *testing.T) {
	var s Store
	s.Add("ann")
	s.Add("bob")
	want := "id,name\n1,ann\n2,bob\n"
	if got := s.ExportCSV(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseCreate(t *testing.T) {
	name, err := ParseCreate("  ann ")
	if err != nil || name != "ann" {
		t.Fatalf("got %q, %v", name, err)
	}
	if _, err := ParseCreate("  "); err == nil {
		t.Fatal("expected error")
	}
}
