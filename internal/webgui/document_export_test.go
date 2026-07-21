package webgui

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentExportDOCX(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/document/export", strings.NewReader(
		`{"format":"docx","filename":"Pismo niemieckie.jpg","text":"# Tłumaczenie\n\nTreść po polsku."}`,
	))
	rec := httptest.NewRecorder()
	new(Server).handleDocumentExport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "wordprocessingml.document") {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, ".docx") {
		t.Fatalf("content disposition = %q", got)
	}
	if _, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len())); err != nil {
		t.Fatalf("response is not a valid docx zip: %v", err)
	}
}

func TestDocumentExportMarkdown(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/document/export", strings.NewReader(
		`{"format":"md","filename":"skan.pdf","text":"# Nagłówek"}`,
	))
	rec := httptest.NewRecorder()
	new(Server).handleDocumentExport(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "# Nagłówek" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "skan.md") {
		t.Fatalf("content disposition = %q", got)
	}
}

func TestDocumentExportRejectsEmptyText(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/document/export", strings.NewReader(
		`{"format":"docx","filename":"empty","text":"  "}`,
	))
	rec := httptest.NewRecorder()
	new(Server).handleDocumentExport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
