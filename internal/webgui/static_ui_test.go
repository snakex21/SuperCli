package webgui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerCustomUIRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<h1>NestCafe bridge</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(nil, false)
	if err := srv.UseUIRoot(root); err != nil {
		t.Fatalf("UseUIRoot: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "NestCafe bridge") {
		t.Fatalf("custom UI response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestServerCustomUIRootRequiresIndex(t *testing.T) {
	srv := NewServer(nil, false)
	if err := srv.UseUIRoot(t.TempDir()); err == nil {
		t.Fatal("UseUIRoot should reject a directory without index.html")
	}
}
