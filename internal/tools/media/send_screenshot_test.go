package media

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeCapture is the test injection for
// ClipboardCapture. The data, mediaType, and
// err fields are returned as-is by Capture.
type fakeCapture struct {
	data      []byte
	mediaType string
	err       error
}

func (f fakeCapture) Capture() ([]byte, string, error) {
	return f.data, f.mediaType, f.err
}

// pngHeader is a real PNG magic header. We
// don't need a full PNG file for these tests
// — just enough bytes to satisfy the magic
// check.
var pngHeader = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}

func TestSendScreenshot_VisionGateRejects(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return false })
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err == nil {
		t.Fatal("expected error for non-vision model")
	}
	if !strings.Contains(res.Err.Error(), "does not support vision") {
		t.Errorf("error text wrong: %v", res.Err)
	}
	if !strings.Contains(res.Err.Error(), "--list-models") {
		t.Errorf("error should hint at --list-models: %v", res.Err)
	}
}

func TestSendScreenshot_VisionGateAccepts(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Image == nil {
		t.Fatal("Image is nil; expected png bytes")
	}
	if res.Image.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", res.Image.MediaType)
	}
	if len(res.Image.Data) != len(pngHeader) {
		t.Errorf("Data length = %d, want %d", len(res.Image.Data), len(pngHeader))
	}
	if !strings.Contains(res.Text, "image/png") {
		t.Errorf("Text missing media type: %s", res.Text)
	}
}

func TestSendScreenshot_EmptyModel(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "model is required") {
		t.Errorf("expected 'model is required'; got %v", res.Err)
	}
}

func TestSendScreenshot_NilHasVision(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, nil) // not wired
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err == nil {
		t.Fatal("expected error when capability gate is not wired")
	}
	if !strings.Contains(res.Err.Error(), "capability gate is not wired") {
		t.Errorf("error should mention the gate; got %v", res.Err)
	}
}

func TestSendScreenshot_CaptureError(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	tool.Capture = fakeCapture{err: os.ErrNotExist}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err == nil {
		t.Fatal("expected capture failure to surface")
	}
}

func TestSendScreenshot_TooLarge(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	tool.MaxBytes = 4 // 4-byte cap; pngHeader is 8 bytes
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "too large") {
		t.Errorf("expected 'too large'; got %v", res.Err)
	}
}

func TestSendScreenshot_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	// Empty media type + non-image bytes →
	// sniffMediaType returns "".
	tool.Capture = fakeCapture{data: []byte("not an image"), mediaType: ""}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "not look like a known image") {
		t.Errorf("expected format error; got %v", res.Err)
	}
}

func TestSendScreenshot_SniffJPEG(t *testing.T) {
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	tool.Capture = fakeCapture{data: jpegHeader, mediaType: ""}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.Image == nil || res.Image.MediaType != "image/jpeg" {
		t.Errorf("expected image/jpeg; got %+v", res.Image)
	}
}

func TestSendScreenshot_SnapshotSaved(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{"model":"gpt-4o"}`))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	// Audit trail file should exist under
	// <BaseDir>/.supercli/snapshots/clipboard-*.png
	snapDir := filepath.Join(dir, ".supercli", "snapshots")
	entries, err := os.ReadDir(snapDir)
	if err != nil {
		t.Fatalf("snapshot dir missing: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 snapshot; got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".png") {
		t.Errorf("snapshot ext = %q, want .png", entries[0].Name())
	}
	if !strings.Contains(res.Text, entries[0].Name()) {
		t.Errorf("Text missing snapshot filename: %s", res.Text)
	}
}

func TestSendScreenshot_RegisteredInRegistry(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	r := NewRegistry()
	if err := r.Register(tool.Spec()); err != nil {
		t.Fatal(err)
	}
	// Use a model the gate accepts and a fake
	// capture that returns the PNG header.
	tool.Capture = fakeCapture{data: pngHeader, mediaType: "image/png"}
	got, _ := r.Execute(context.Background(), "send_screenshot", json.RawMessage(`{"model":"gpt-4o"}`))
	if got.Err != nil {
		t.Errorf("unexpected err: %v", got.Err)
	}
	if got.Image == nil {
		t.Error("Image is nil; expected attached bytes")
	}
}

func TestSendScreenshot_Spec(t *testing.T) {
	dir := t.TempDir()
	tool := NewSendScreenshot(dir, func(id string) bool { return true })
	spec := tool.Spec()
	if spec.Name != "send_screenshot" {
		t.Errorf("Name = %q, want send_screenshot", spec.Name)
	}
	if spec.Fn == nil {
		t.Error("Fn is nil")
	}
}

func TestSendScreenshot_SniffMediaType(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"PNG", pngHeader, "image/png"},
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, "image/jpeg"},
		{"GIF87", []byte("GIF87a..."), "image/gif"},
		{"GIF89", []byte("GIF89a..."), "image/gif"},
		{"BMP", []byte("BM..."), "image/bmp"},
		{"WebP", []byte("RIFF????WEBP"), "image/webp"},
		{"unknown", []byte("plain text"), ""},
		{"empty", nil, ""},
		{"too short", []byte{1, 2}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sniffMediaType(c.data)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSendScreenshot_MediaTypeExt(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
		{"image/bmp", ".bmp"},
		{"image/tiff", ".tiff"},
		{"unknown", ".bin"},
	}
	for _, c := range cases {
		if got := mediaTypeExt(c.in); got != c.want {
			t.Errorf("mediaTypeExt(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
