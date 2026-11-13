package users

import "testing"

func TestStoreExport(t *testing.T) {
	var s Store
	s.Add("ann", "ann@x.io")
	s.Add("bob", "bob@x.io")
	want := "id,name,email\n1,ann,ann@x.io\n2,bob,bob@x.io\n"
	if got := s.ExportCSV(); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseCreate(t *testing.T) {
	name, email, err := ParseCreate("  ann ,ann@x.io")
	if err != nil || name != "ann" || email != "ann@x.io" {
		t.Fatalf("got %q %q, %v", name, email, err)
	}
	if _, _, err := ParseCreate("ann,nomail"); err == nil {
		t.Fatal("expected error")
	}
}
