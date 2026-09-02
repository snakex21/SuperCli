//go:build windows

package webgui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHardProtocolNativeWebViewUsesPortableProfile(t *testing.T) {
	dataDir := t.TempDir()
	const original = `C:\external-appdata`
	t.Setenv("APPDATA", original)
	called := false
	err := withPortableWebViewProfile(dataDir, func() error {
		called = true
		want := filepath.Join(dataDir, "browser-profile")
		if got := os.Getenv("APPDATA"); got != want {
			t.Fatalf("APPDATA during WebView creation=%q, want %q", got, want)
		}
		if info, statErr := os.Stat(want); statErr != nil || !info.IsDir() {
			t.Fatalf("portable profile root missing: info=%v err=%v", info, statErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("WebView opener was not called")
	}
	if got := os.Getenv("APPDATA"); got != original {
		t.Fatalf("APPDATA was not restored: %q", got)
	}
}
