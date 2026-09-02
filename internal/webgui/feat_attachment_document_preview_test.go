package webgui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/office"
)

func TestAttachmentDocumentPreviewReadsDocx(t *testing.T) {
	srv := newTestServer(t, false)
	path := filepath.Join(srv.eng.Home(), "podglad.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := office.WriteSimpleDocx(file, "# Dokument\n\nTreść podglądu Word."); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.handleAttachmentDocumentPreview(rec, httptest.NewRequest(
		http.MethodGet,
		"/api/attachment/document-preview?path="+url.QueryEscape(path),
		nil,
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Name != "podglad.docx" || !strings.Contains(result.Text, "Treść podglądu Word") {
		t.Fatalf("preview=%+v", result)
	}
}

func TestAttachmentDocumentPreviewRejectsOutsideWorkspace(t *testing.T) {
	srv := newTestServer(t, false)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleAttachmentDocumentPreview(rec, httptest.NewRequest(
		http.MethodGet,
		"/api/attachment/document-preview?path="+url.QueryEscape(outside),
		nil,
	))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
