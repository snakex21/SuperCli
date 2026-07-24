package webgui

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentUploadAndImagePreview(t *testing.T) {
	srv := newTestServer(t, false)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "clipboard.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nimage"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/attachment/upload", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploaded := httptest.NewRecorder()
	srv.handleAttachmentUpload(uploaded, upload)
	if uploaded.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
	var result struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(uploaded.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || !strings.Contains(result.Paths[0], filepath.Join(".supercli", "attachments")) {
		t.Fatalf("uploaded paths = %#v", result.Paths)
	}
	preview := httptest.NewRecorder()
	srv.handleAttachmentPreview(preview, httptest.NewRequest(
		http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(result.Paths[0]), nil,
	))
	if preview.Code != http.StatusOK || preview.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("preview status=%d type=%q body=%s", preview.Code, preview.Header().Get("Content-Type"), preview.Body.String())
	}
}

func TestAttachmentPreviewRejectsText(t *testing.T) {
	srv := newTestServer(t, false)
	path := filepath.Join(srv.eng.Home(), "notes.txt")
	if err := os.WriteFile(path, []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.handleAttachmentPreview(rec, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(path), nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProfileAttachmentPreviewSurvivesWorkspaceChange(t *testing.T) {
	srv := newTestServer(t, false)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "history.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nprofile-image"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest(http.MethodPost, "/api/attachment/upload?scope=profile", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	uploaded := httptest.NewRecorder()
	srv.handleAttachmentUpload(uploaded, upload)
	if uploaded.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", uploaded.Code, uploaded.Body.String())
	}
	var result struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(uploaded.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || !strings.Contains(result.Paths[0], "module-sources") {
		t.Fatalf("profile paths = %#v", result.Paths)
	}

	srv.eng.setHome(t.TempDir())
	preview := httptest.NewRecorder()
	srv.handleAttachmentPreview(preview, httptest.NewRequest(
		http.MethodGet, "/api/attachment/preview?scope=profile&path="+url.QueryEscape(result.Paths[0]), nil,
	))
	if preview.Code != http.StatusOK || preview.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("preview status=%d type=%q body=%s", preview.Code, preview.Header().Get("Content-Type"), preview.Body.String())
	}
}
