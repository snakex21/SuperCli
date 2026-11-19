package session

import (
	"context"
	"testing"
)

func TestStoreRenameProviderUpdatesSessionsAndContextModels(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.Create(dir, "m1", "a")
	b, _ := s.Create(dir, "m2", "b")
	if err := s.SetRuntime(a.ID, "work", "m1", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRuntime(b.ID, "home", "m2", ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := NewWriter(s, a.ID).SaveContextModel(ctx, "work", "m1"); err != nil {
		t.Fatal(err)
	}
	n, err := s.RenameProvider("work", "Work Cloud")
	if err != nil || n != 1 {
		t.Fatalf("RenameProvider = %d, %v", n, err)
	}
	got, _ := s.Get(a.ID)
	other, _ := s.Get(b.ID)
	if got.Provider != "Work Cloud" || other.Provider != "home" {
		t.Fatalf("providers = %q, %q", got.Provider, other.Provider)
	}
	if provider, _, err := NewWriter(s, a.ID).ReadContextModel(ctx); err != nil || provider != "Work Cloud" {
		t.Fatalf("context model provider = %q, %v", provider, err)
	}
	if n, err := s.RenameProvider("work", "x"); err != nil || n != 0 {
		t.Fatalf("second rename = %d, %v", n, err)
	}
}
