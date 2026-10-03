package codexauth

import (
	"os"
	"testing"
	"time"
)

func TestImportedWindowsUTF8BOMAuthSupportsAccountDiscovery(t *testing.T) {
	dir := t.TempDir()
	path := AuthFilePathFor(dir, "work")
	af := &AuthFile{LastRefresh: time.Now(), Tokens: &TokenData{AccessToken: "synthetic", AccountID: "synthetic-id"}}
	if err := Save(path, af); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append([]byte{0xef, 0xbb, 0xbf}, raw...), 0600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManagerFor(dir, "work", Options{})
	if !mgr.LoggedIn() {
		t.Fatal("BOM prevented account discovery")
	}
	info, err := mgr.Account()
	if err != nil || !info.LoggedIn || info.AccountID != "synthetic-id" {
		t.Fatal("BOM import metadata unavailable")
	}
}
