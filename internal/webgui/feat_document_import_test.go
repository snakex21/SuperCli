package webgui

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/tools/office"
)

func TestDocumentImportMarkdown(t *testing.T) {
	srv := newTestServer(t, false)
	rec := importDocumentForTest(t, srv, "note.md", []byte("# Tytuł\n\nTreść."))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["source_format"] != "md" || !strings.Contains(result["text"].(string), "# Tytuł") {
		t.Fatalf("result=%v", result)
	}
}

func TestDocumentImportDOCX(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := office.WriteSimpleDocx(file, "# Tytuł\n\nTreść z Worda."); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, false)
	rec := importDocumentForTest(t, srv, "input.docx", data)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Treść z Worda") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func importDocumentForTest(t *testing.T, srv *Server, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/document/import", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.handleDocumentImport(rec, req)
	return rec
}
