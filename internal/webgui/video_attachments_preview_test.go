package webgui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"supercli/internal/llm"
)

func TestVideoQuickTimePreviewUsesMagicAndRanges(t *testing.T) {
	home := t.TempDir()
	server := &Server{eng: &Engine{home: home}}
	data := append([]byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00qt  "), bytes.Repeat([]byte("fixture"), 128)...)
	path := filepath.Join(home, "recording.mov")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/attachment/preview?path="+url.QueryEscape(path), nil)
	request.Header.Set("Range", "bytes=20-49")
	response := httptest.NewRecorder()
	server.handleAttachmentPreview(response, request)
	if response.Code != http.StatusPartialContent || response.Header().Get("Content-Type") != "video/quicktime" || !bytes.Equal(response.Body.Bytes(), data[20:50]) || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	if err := os.WriteFile(path, []byte("not a QuickTime video"), 0600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	server.handleAttachmentPreview(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("extension-only MOV accepted: %d", response.Code)
	}
}

// Hiding the optional method represents an unknown capability, not text-only.
type videoUnknownProvider struct{ llm.Provider }

func TestVideoAttachmentProviderCapabilityContract(t *testing.T) {
	p := &videoFixtureProvider{}
	images := []llm.ImageRef{{MediaType: "image/jpeg", Data: "fixture"}}
	got, note := attachmentInputsForProvider(p, images, "addon")
	if len(got) != 0 || !strings.Contains(note, "text-only") {
		t.Fatal("confirmed text-only received pixels or no warning")
	}
	got, note = attachmentInputsForProvider(videoUnknownProvider{p}, images, "addon")
	if len(got) != 1 || note != "addon" {
		t.Fatal("unknown capability was treated as confirmed text-only")
	}
	p.vision.Store(true)
	got, note = attachmentInputsForProvider(p, images, "addon")
	if len(got) != 1 || note != "addon" {
		t.Fatal("vision provider lost existing image inputs")
	}
}
