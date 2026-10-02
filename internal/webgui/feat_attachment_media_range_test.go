package webgui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestMediaPreviewRangeStreamingAndType(t *testing.T) {
	home := t.TempDir()
	data := append([]byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), bytes.Repeat([]byte("test"), 2048)...)
	path := filepath.Join(home, "preview.mp4")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	server := &Server{eng: &Engine{home: home}}
	req := httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(path), nil)
	req.Header.Set("Range", "bytes=24-127")
	w := httptest.NewRecorder()
	server.handleAttachmentPreview(w, req)
	if w.Code != http.StatusPartialContent || w.Header().Get("Content-Type") != "video/mp4" || !bytes.Equal(w.Body.Bytes(), data[24:128]) {
		t.Fatalf("status=%d headers=%v bytes=%d", w.Code, w.Header(), w.Body.Len())
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal(w.Header())
	}
	// The MIME gate is independent of a misleading extension.
	text := filepath.Join(home, "fake.mp4")
	os.WriteFile(text, []byte("ordinary text"), 0600)
	w = httptest.NewRecorder()
	server.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(text), nil))
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("fake media status=%d", w.Code)
	}
}

func TestPortableSnapshotPreviewOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	home, dataDir := filepath.Join(root, "workspace"), filepath.Join(root, "app-data")
	os.MkdirAll(home, 0700)
	dir := filepath.Join(dataDir, ".supercli", "snapshots")
	os.MkdirAll(dir, 0700)
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	path := filepath.Join(dir, "screen-123.png")
	os.WriteFile(path, png, 0600)
	server := &Server{eng: &Engine{home: home, dataDir: dataDir}}
	for _, ref := range []string{path, "snapshot:screen-123.png"} {
		w := httptest.NewRecorder()
		server.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(ref), nil))
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) {
			t.Fatalf("ref=%s status=%d body=%s", ref, w.Code, w.Body.String())
		}
	}
	// The portable namespace can never expose unrelated application data.
	secret := filepath.Join(dataDir, "config.png")
	os.WriteFile(secret, png, 0600)
	for _, ref := range []string{secret, "snapshot:../../config.png", "snapshot:../missing.png"} {
		w := httptest.NewRecorder()
		server.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(ref), nil))
		if w.Code != http.StatusForbidden {
			t.Fatalf("escaped ref=%s status=%d", ref, w.Code)
		}
	}
	// Moving the portable folder keeps the same transcript preview reference.
	moved := filepath.Join(root, "moved-app")
	if err := os.Rename(dataDir, moved); err != nil {
		t.Fatal(err)
	}
	server.eng.dataDir = moved
	w := httptest.NewRecorder()
	server.handleAttachmentPreview(w, httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path=snapshot:screen-123.png", nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("moved status=%d", w.Code)
	}
}
