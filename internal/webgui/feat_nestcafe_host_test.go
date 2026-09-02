package webgui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledThunderbirdXPIIsServedFromNestCafeRoot(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "supercli-data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine(echoConfig(), t.TempDir(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	srv := NewServer(eng, false)
	integrations := filepath.Join(root, "integrations")
	if err := os.MkdirAll(integrations, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake-xpi")
	path := filepath.Join(integrations, "Thunderbird-AI-Bridge.xpi")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := srv.bundledThunderbirdXPI(); got != path {
		t.Fatalf("bundled path=%q want %q", got, path)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/thunderbird/xpi", nil)
	srv.handleThunderbirdXPI(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != string(payload) {
		t.Fatalf("xpi response status=%d body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "Thunderbird-AI-Bridge.xpi") {
		t.Fatalf("content disposition=%q", rec.Header().Get("Content-Disposition"))
	}
}

func TestNewerNestCafeVersion(t *testing.T) {
	cases := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"2.0.3", "2.0.2", true},
		{"v2.1.0", "2.0.9", true},
		{"2.0.2", "2.0.2", false},
		{"2.0.1", "2.0.2", false},
		{"3.0.0", "2.9.9", true},
	}
	for _, tc := range cases {
		if got := newerNestCafeVersion(tc.candidate, tc.current); got != tc.want {
			t.Fatalf("newerNestCafeVersion(%q,%q)=%v want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}
