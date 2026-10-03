package codexauth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCatalogIdentityChangesOnLoginReplacementAndDoesNotExposeCredentials(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManagerFor(dir, "fixture", Options{})
	af := &AuthFile{LastRefresh: time.Now(), Tokens: &TokenData{AccessToken: "first-fixture-token", AccountID: "fixture-account"}}
	if err := Save(mgr.Path(), af); err != nil {
		t.Fatal(err)
	}
	first, err := mgr.CatalogIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || strings.Contains(first, "fixture") {
		t.Fatal("identity is not an opaque digest")
	}
	af.Tokens.AccessToken = "replaced-fixture-token"
	if err := Save(mgr.Path(), af); err != nil {
		t.Fatal(err)
	}
	second, err := mgr.CatalogIdentity()
	if err != nil || first == second {
		t.Fatal("replacement reused account metadata identity")
	}
	if err := mgr.Logout(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CatalogIdentity(); err == nil {
		t.Fatal("logged-out identity remained available")
	}
}

func TestTokenRecoversAccountIDFromImportedJWT(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManagerFor(dir, "fixture", Options{})
	if err := Save(mgr.Path(), &AuthFile{LastRefresh: time.Now(), Tokens: &TokenData{AccessToken: "fixture-token", IDToken: fakeJWT(t, "fixture-account")}}); err != nil {
		t.Fatal(err)
	}
	_, account, err := mgr.Token(context.Background())
	if err != nil || account != "fixture-account" {
		t.Fatalf("imported account routing metadata missing: %q/%v", account, err)
	}
}
