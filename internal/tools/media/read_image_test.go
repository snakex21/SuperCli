package media

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writePNG writes a minimal 1x1 transparent PNG to a temp file.
func writePNG(t *testing.T, dir, name string) string {
	t.Helper()
	// 1x1 transparent PNG (67 bytes).
	png := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
		0x89, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
		0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
		0x42, 0x60, 0x82,
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, png, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return p
}

// writeJPEG writes a minimal JPEG (SOI + APP0 + EOI).
func writeJPEG(t *testing.T, dir, name string) string {
	t.Helper()
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, jpg, 0o644); err != nil {
		t.Fatalf("write jpg: %v", err)
	}
	return p
}

func TestReadImage_LoadsPNG(t *testing.T) {
	dir := t.TempDir()
	_ = writePNG(t, dir, "cat.png")
	tool := NewReadImage(dir, 0)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"cat.png"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err = %v", res.Err)
	}
	if res.Image == nil {
		t.Fatal("Image is nil")
	}
	if res.Image.MediaType != "image/png" {
		t.Fatalf("MediaType = %q", res.Image.MediaType)
	}
	if len(res.Image.Data) == 0 {
		t.Fatal("empty data")
	}
	if !contains(res.Text, "cat.png") {
		t.Fatalf("Text should mention filename: %q", res.Text)
	}
}

func TestReadImage_LoadsJPEG(t *testing.T) {
	dir := t.TempDir()
	_ = writeJPEG(t, dir, "x.jpg")
	tool := NewReadImage(dir, 0)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"x.jpg"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Image.MediaType != "image/jpeg" {
		t.Fatalf("MediaType = %q", res.Image.MediaType)
	}
}

func TestReadImage_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	img := writePNG(t, dir, "abs.png")
	tool := NewReadImage(".", 0)
	// Build JSON body as a string (not raw literal) so the
	// backslashes in the Windows path survive.
	body := `{"path":"` + filepath.ToSlash(img) + `"}`
	res, err := tool.Execute(context.Background(), json.RawMessage(body))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Image == nil {
		t.Fatal("Image is nil")
	}
}

func TestReadImage_MissingPath(t *testing.T) {
	tool := NewReadImage(".", 0)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error on missing path")
	}
}

func TestReadImage_EmptyPath(t *testing.T) {
	tool := NewReadImage(".", 0)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":""}`))
	if err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestReadImage_NotAnImage(t *testing.T) {
	dir := t.TempDir()
	txt := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(txt, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tool := NewReadImage(dir, 0)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"note.txt"}`))
	if err == nil {
		t.Fatal("expected error on non-image file")
	}
}

func TestReadImage_TooLarge(t *testing.T) {
	dir := t.TempDir()
	img := writePNG(t, dir, "big.png")
	_ = img
	// Set max to less than the PNG size.
	tool := NewReadImage(dir, 10)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"big.png"}`))
	if err == nil {
		t.Fatal("expected error on oversize file")
	}
}

func TestReadImage_Directory(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadImage(dir, 0)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"."}`))
	if err == nil {
		t.Fatal("expected error on directory")
	}
}

func TestReadImage_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadImage(dir, 0)
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"nonexistent.png"}`))
	if err == nil {
		t.Fatal("expected error on missing file")
	}
}

func TestReadImage_BadJSON(t *testing.T) {
	tool := NewReadImage(".", 0)
	_, err := tool.Execute(context.Background(), json.RawMessage(`not json`))
	if err == nil {
		t.Fatal("expected error on bad json")
	}
}

func TestReadImage_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewReadImage(".", 0)
	_, err := tool.Execute(ctx, json.RawMessage(`{"path":"x"}`))
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestReadImage_Spec(t *testing.T) {
	tool := NewReadImage(".", 0)
	spec := tool.Spec()
	if spec.Name != "read_image" {
		t.Fatalf("Name = %q", spec.Name)
	}
	if spec.Fn == nil {
		t.Fatal("Fn is nil")
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestReadImage_RegisteredInRegistry(t *testing.T) {
	tool := NewReadImage(".", 0)
	r := NewRegistry()
	if err := r.Register(tool.Spec()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	dir := t.TempDir()
	png := writePNG(t, dir, "y.png")
	body := `{"path":"` + filepath.ToSlash(png) + `"}`
	res, err := r.Execute(context.Background(), "read_image", json.RawMessage(body))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Image == nil {
		t.Fatal("Image is nil")
	}
}

func TestDetectImageMIME(t *testing.T) {
	// Each case must be at least 12 bytes so the length check
	// in detectImageMIME does not bail early.
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0}
	jpg := []byte{0xFF, 0xD8, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	gif := []byte("GIF89a..............") // 20 bytes
	webp := []byte("RIFF\x00\x00\x00\x00WEBP............")
	plain := []byte("plain text.............") // 25 bytes, no magic
	cases := []struct {
		in   []byte
		want string
	}{
		{png, "image/png"},
		{jpg, "image/jpeg"},
		{gif, "image/gif"},
		{webp, "image/webp"},
		{plain, ""},
		{[]byte{}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		got := detectImageMIME(c.in)
		if got != c.want {
			t.Errorf("detectImageMIME(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSupportedImageMIMEs(t *testing.T) {
	mimes := SupportedImageMIMEs()
	if len(mimes) != 4 {
		t.Fatalf("len = %d, want 4", len(mimes))
	}
}

// contains is a tiny helper (strings.Contains is also fine but
// kept here to make test file self-contained).
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
